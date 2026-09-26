package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/blueship581/hydropower-gate-dispatch-control/backend/internal/config"
	"github.com/blueship581/hydropower-gate-dispatch-control/backend/internal/dto"
	"github.com/blueship581/hydropower-gate-dispatch-control/backend/internal/model"
	"github.com/blueship581/hydropower-gate-dispatch-control/backend/internal/repository"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newDirectiveService(t *testing.T) (OperationDirectiveService, *gorm.DB) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("database handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&model.Reservoir{}, &model.GateUnit{}, &model.OperationDirective{}, &model.DirectiveApproval{}, &model.AuditLog{}); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	gate := model.GateUnit{BaseModel: model.BaseModel{Code: "GU-TEST", Name: "右岸泄洪闸", Status: "closed", Version: 1}, Facility: "右岸坝段", Owner: "运行一组"}
	if err := db.Create(&gate).Error; err != nil {
		t.Fatalf("create test gate: %v", err)
	}
	security := NewSecurityService(repository.NewSecurityRepository(db), config.Config{})
	return NewOperationDirectiveService(repository.NewOperationDirectiveRepository(db), repository.NewGateUnitRepository(db), repository.NewReservoirRepository(db), security), db
}

func directiveInput(code string) dto.CreateOperationDirective {
	return dto.CreateOperationDirective{
		Code: code, Name: "右岸泄洪闸调度", Description: "测试双人确认和状态证据",
		Facility: "右岸坝段", Owner: "运行一组", Category: "泄洪调度", RiskLevel: "high",
		MetricValue: 35, MetricUnit: "%", EffectiveAt: time.Now().UTC().Add(time.Hour),
		Evidence: "水位 168.2m，处于许可窗口", RelatedCode: "GU-TEST", GateState: "closed",
	}
}

func TestDirectiveRequiresIndependentReviewerAndPreservesEvidence(t *testing.T) {
	service, db := newDirectiveService(t)
	ctx := context.Background()
	created, err := service.Create(ctx, directiveInput("OD-TEST-1"), "operator", "req-create")
	if err != nil {
		t.Fatalf("create directive: %v", err)
	}
	submitted, err := service.Transition(ctx, created.ID, dto.TransitionRequest{
		Status: "pending", ExpectedVersion: created.Version, Reason: "提交水位窗口和开度计划复核",
	}, "operator", model.RoleOperator, "req-submit")
	if err != nil {
		t.Fatalf("submit directive: %v", err)
	}
	if submitted.SubmittedBy != "operator" || len(submitted.Approvals) != 1 || submitted.Approvals[0].RequestID != "req-submit" {
		t.Fatalf("submission evidence not preserved: %#v", submitted)
	}

	_, err = service.Transition(ctx, submitted.ID, dto.TransitionRequest{
		Status: "approved", ExpectedVersion: submitted.Version, Reason: "操作员不得自行批准",
	}, "operator", model.RoleOperator, "req-self-operator")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("operator approval should be forbidden, got %v", err)
	}
	_, err = service.Transition(ctx, submitted.ID, dto.TransitionRequest{
		Status: "approved", ExpectedVersion: submitted.Version, Reason: "同一账号不得切换角色自批",
	}, "operator", model.RoleReviewer, "req-self-reviewer")
	if !errors.Is(err, ErrTwoPersonRequired) {
		t.Fatalf("same-account approval should fail two-person rule, got %v", err)
	}

	approved, err := service.Transition(ctx, submitted.ID, dto.TransitionRequest{
		Status: "approved", ExpectedVersion: submitted.Version, Reason: "复核闸门目标、水位窗口及证据一致",
	}, "reviewer", model.RoleReviewer, "req-approve")
	if err != nil {
		t.Fatalf("reviewer approve directive: %v", err)
	}
	if approved.ApprovedBy != "reviewer" || approved.SubmittedBy == approved.ApprovedBy || len(approved.Approvals) != 2 {
		t.Fatalf("two-person evidence invalid: %#v", approved)
	}
	if approved.Approvals[1].Stage != "approved" || approved.Approvals[1].RequestID != "req-approve" {
		t.Fatalf("approval trail invalid: %#v", approved.Approvals)
	}
	var transitions int64
	if err := db.Model(&model.AuditLog{}).Where("entity_type = ? AND entity_id = ? AND action = ?", "OperationDirective", approved.ID, "transition").Count(&transitions).Error; err != nil {
		t.Fatalf("count transition audits: %v", err)
	}
	if transitions != 2 {
		t.Fatalf("expected two transition audits, got %d", transitions)
	}

	_, err = service.Update(ctx, approved.ID, dto.UpdateOperationDirective{ExpectedVersion: approved.Version}, "operator", "req-edit")
	if !errors.Is(err, ErrImmutableState) {
		t.Fatalf("submitted directive must be immutable, got %v", err)
	}
}

func TestDirectiveTransitionRollsBackWhenAuditCannotPersist(t *testing.T) {
	service, db := newDirectiveService(t)
	ctx := context.Background()
	created, err := service.Create(ctx, directiveInput("OD-TEST-ROLLBACK"), "operator", "req-create")
	if err != nil {
		t.Fatalf("create directive: %v", err)
	}
	if err := db.Migrator().DropTable(&model.AuditLog{}); err != nil {
		t.Fatalf("drop audit table: %v", err)
	}
	_, err = service.Transition(ctx, created.ID, dto.TransitionRequest{
		Status: "pending", ExpectedVersion: created.Version, Reason: "应与审计失败一起回滚",
	}, "operator", model.RoleOperator, "req-rollback")
	if err == nil {
		t.Fatal("transition should fail when audit cannot persist")
	}
	stored, getErr := service.Get(ctx, created.ID)
	if getErr != nil {
		t.Fatalf("reload rolled-back directive: %v", getErr)
	}
	if stored.Status != "draft" || stored.Version != created.Version || len(stored.Approvals) != 0 {
		t.Fatalf("transition was not fully rolled back: %#v", stored)
	}
}

func TestDirectiveCreateRollsBackWhenAuditCannotPersist(t *testing.T) {
	service, db := newDirectiveService(t)
	if err := db.Migrator().DropTable(&model.AuditLog{}); err != nil {
		t.Fatalf("drop audit table: %v", err)
	}
	_, err := service.Create(context.Background(), directiveInput("OD-CREATE-ROLLBACK"), "operator", "req-create-rollback")
	if err == nil {
		t.Fatal("create should fail when audit cannot persist")
	}
	var count int64
	if err := db.Model(&model.OperationDirective{}).Where("code = ?", "OD-CREATE-ROLLBACK").Count(&count).Error; err != nil {
		t.Fatalf("count directives: %v", err)
	}
	if count != 0 {
		t.Fatalf("directive persisted without audit: count=%d", count)
	}
}

// setupWaterWindowScenario 建立带水位许可区间的库区、所属闸门和一条已复核指令，
// 用于验证已复核推进执行时的水位放行判定。
func setupWaterWindowScenario(t *testing.T, reservoirStatus string, level float64, min, max *float64, gateState string) (OperationDirectiveService, model.OperationDirective) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("database handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&model.Reservoir{}, &model.GateUnit{}, &model.OperationDirective{}, &model.DirectiveApproval{}, &model.AuditLog{}); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	reservoir := model.Reservoir{
		BaseModel: model.BaseModel{Code: "R-WIN", Name: "水位窗口库区", Status: reservoirStatus, Version: 1},
		Facility:  "右岸坝段", Owner: "运行一组", MetricValue: level, MetricUnit: "m",
		WaterLevelMin: min, WaterLevelMax: max,
	}
	if err := db.Create(&reservoir).Error; err != nil {
		t.Fatalf("create test reservoir: %v", err)
	}
	gate := model.GateUnit{BaseModel: model.BaseModel{Code: "GU-WIN", Name: "泄洪闸", Status: "closed", Version: 1}, Facility: "右岸坝段", Owner: "运行一组", RelatedCode: "R-WIN"}
	if err := db.Create(&gate).Error; err != nil {
		t.Fatalf("create test gate: %v", err)
	}
	security := NewSecurityService(repository.NewSecurityRepository(db), config.Config{})
	service := NewOperationDirectiveService(repository.NewOperationDirectiveRepository(db), repository.NewGateUnitRepository(db), repository.NewReservoirRepository(db), security)
	ctx := context.Background()
	created, err := service.Create(ctx, dto.CreateOperationDirective{
		Code: "OD-WIN", Name: "水位窗口验证指令", Description: "验证水位许可区间放行判定",
		Facility: "右岸坝段", Owner: "运行一组", Category: "泄洪调度", RiskLevel: "high",
		MetricValue: 50, MetricUnit: "%", EffectiveAt: time.Now().UTC().Add(time.Hour),
		Evidence: "水位窗口测试证据", RelatedCode: "GU-WIN", GateState: gateState,
	}, "operator", "req-create")
	if err != nil {
		t.Fatalf("create directive: %v", err)
	}
	submitted, err := service.Transition(ctx, created.ID, dto.TransitionRequest{
		Status: "pending", ExpectedVersion: created.Version, Reason: "提交水位窗口复核",
	}, "operator", model.RoleOperator, "req-submit")
	if err != nil {
		t.Fatalf("submit directive: %v", err)
	}
	approved, err := service.Transition(ctx, submitted.ID, dto.TransitionRequest{
		Status: "approved", ExpectedVersion: submitted.Version, Reason: "复核水位窗口与闸门目标",
	}, "reviewer", model.RoleReviewer, "req-approve")
	if err != nil {
		t.Fatalf("approve directive: %v", err)
	}
	return service, approved
}

func TestDirectiveExecutionRespectsReservoirWaterLevelWindow(t *testing.T) {
	windowMin, windowMax := 165.0, 172.0
	cases := []struct {
		name            string
		reservoirStatus string
		level           float64
		min             *float64
		max             *float64
		gateState       string
		wantBlocked     bool
		wantReasonParts []string
	}{
		{name: "水位在区间内开闸放行", reservoirStatus: "normal", level: 168.5, min: &windowMin, max: &windowMax, gateState: "open"},
		{name: "越过上限开闸拦截", reservoirStatus: "normal", level: 173.2, min: &windowMin, max: &windowMax, gateState: "open", wantBlocked: true, wantReasonParts: []string{"上限", "173.20", "172.00"}},
		{name: "越过上限关闸照旧放行", reservoirStatus: "normal", level: 173.2, min: &windowMin, max: &windowMax, gateState: "closed"},
		{name: "低于下限开闸拦截", reservoirStatus: "normal", level: 164.0, min: &windowMin, max: &windowMax, gateState: "open", wantBlocked: true, wantReasonParts: []string{"下限", "164.00", "165.00"}},
		{name: "低于下限关闸同样拦截", reservoirStatus: "normal", level: 164.0, min: &windowMin, max: &windowMax, gateState: "closed", wantBlocked: true, wantReasonParts: []string{"下限"}},
		{name: "受限库区开闸一律拒绝", reservoirStatus: "restricted", level: 168.5, min: &windowMin, max: &windowMax, gateState: "open", wantBlocked: true, wantReasonParts: []string{"受限", "168.50"}},
		{name: "受限库区关闸放行", reservoirStatus: "restricted", level: 168.5, min: &windowMin, max: &windowMax, gateState: "closed"},
		{name: "未配置区间按现状放行", reservoirStatus: "normal", level: 190.0, gateState: "open"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service, approved := setupWaterWindowScenario(t, tc.reservoirStatus, tc.level, tc.min, tc.max, tc.gateState)
			ctx := context.Background()
			executed, err := service.Transition(ctx, approved.ID, dto.TransitionRequest{
				Status: "executing", ExpectedVersion: approved.Version, Reason: "双人许可完成，现场开始执行",
			}, "operator", model.RoleOperator, "req-execute")
			if !tc.wantBlocked {
				if err != nil {
					t.Fatalf("expected execution to proceed, got %v", err)
				}
				if executed.Status != "executing" {
					t.Fatalf("expected executing, got %s", executed.Status)
				}
				return
			}
			if !errors.Is(err, ErrExecutionBlocked) {
				t.Fatalf("expected ErrExecutionBlocked, got %v", err)
			}
			for _, part := range tc.wantReasonParts {
				if !strings.Contains(err.Error(), part) {
					t.Fatalf("block reason %q should mention %q", err.Error(), part)
				}
			}
			stored, getErr := service.Get(ctx, approved.ID)
			if getErr != nil {
				t.Fatalf("reload blocked directive: %v", getErr)
			}
			if stored.Status != "approved" {
				t.Fatalf("blocked directive must stay approved, got %s", stored.Status)
			}
		})
	}
}

func TestDirectiveListExposesReservoirWaterLevelAndPermission(t *testing.T) {
	windowMin, windowMax := 165.0, 172.0
	service, approved := setupWaterWindowScenario(t, "normal", 173.2, &windowMin, &windowMax, "open")
	page, err := service.List(context.Background(), dto.PageQuery{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("list directives: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("expected one directive, got %d", len(page.Items))
	}
	item := page.Items[0]
	if item.ID != approved.ID || item.ReservoirCode != "R-WIN" {
		t.Fatalf("reservoir context missing: %#v", item)
	}
	if item.ReservoirWaterLevel == nil || *item.ReservoirWaterLevel != 173.2 || item.ReservoirWaterUnit != "m" {
		t.Fatalf("water level not exposed: %#v", item)
	}
	if item.ExecutionPermitted == nil || *item.ExecutionPermitted {
		t.Fatalf("execution above the upper window bound must not be permitted: %#v", item)
	}
	if !strings.Contains(item.ExecutionBlockReason, "上限") || !strings.Contains(item.ExecutionBlockReason, "173.20") {
		t.Fatalf("block reason should name the bound and the level, got %q", item.ExecutionBlockReason)
	}
}
