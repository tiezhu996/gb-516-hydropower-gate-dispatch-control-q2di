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
	lower, upper := 165.0, 175.0
	reservoir := model.Reservoir{BaseModel: model.BaseModel{Code: "R-TEST", Name: "右岸库区", Status: "normal", Version: 1}, Facility: "右岸坝段", Owner: "运行一组", WaterLevel: 170.0, WaterLevelLower: &lower, WaterLevelUpper: &upper}
	if err := db.Create(&reservoir).Error; err != nil {
		t.Fatalf("create test reservoir: %v", err)
	}
	gate := model.GateUnit{BaseModel: model.BaseModel{Code: "GU-TEST", Name: "右岸泄洪闸", Status: "closed", Version: 1}, Facility: "右岸坝段", Owner: "运行一组", RelatedCode: "R-TEST"}
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

// approveDirective 走完整 draft → pending → approved 双人复核流程。
func approveDirective(t *testing.T, svc OperationDirectiveService, code, gateState string) model.OperationDirective {
	t.Helper()
	ctx := context.Background()
	input := directiveInput(code)
	input.GateState = gateState
	created, err := svc.Create(ctx, input, "operator", "req-create")
	if err != nil {
		t.Fatalf("create directive: %v", err)
	}
	submitted, err := svc.Transition(ctx, created.ID, dto.TransitionRequest{Status: "pending", ExpectedVersion: created.Version, Reason: "提交复核"}, "operator", model.RoleOperator, "req-submit")
	if err != nil {
		t.Fatalf("submit directive: %v", err)
	}
	approved, err := svc.Transition(ctx, submitted.ID, dto.TransitionRequest{Status: "approved", ExpectedVersion: submitted.Version, Reason: "复核通过"}, "reviewer", model.RoleReviewer, "req-approve")
	if err != nil {
		t.Fatalf("approve directive: %v", err)
	}
	return approved
}

func updateTestReservoir(t *testing.T, db *gorm.DB, status string, waterLevel float64, lower, upper *float64) {
	t.Helper()
	if err := db.Model(&model.Reservoir{}).Where("code = ?", "R-TEST").
		Updates(map[string]any{"status": status, "water_level": waterLevel, "water_level_lower": lower, "water_level_upper": upper}).Error; err != nil {
		t.Fatalf("update test reservoir: %v", err)
	}
}

func TestExecuteBlockedAboveUpperStaysApprovedForOpenDirective(t *testing.T) {
	svc, db := newDirectiveService(t)
	approved := approveDirective(t, svc, "OD-ABOVE-UPPER", "open")
	updateTestReservoir(t, db, "warning", 176.2, ptrFloat(165.0), ptrFloat(175.0))

	_, err := svc.Transition(context.Background(), approved.ID, dto.TransitionRequest{
		Status: "executing", ExpectedVersion: approved.Version, Reason: "越上限开闸应被挡住",
	}, "operator", model.RoleOperator, "req-execute-blocked")
	if !errors.Is(err, ErrWaterLevelBlocked) {
		t.Fatalf("above-upper open should be blocked by water level permit, got %v", err)
	}
	if !strings.Contains(err.Error(), "176.20") || !strings.Contains(err.Error(), "175.00") {
		t.Fatalf("block reason must state window and level: %v", err)
	}
	stored, getErr := svc.Get(context.Background(), approved.ID)
	if getErr != nil {
		t.Fatalf("reload blocked directive: %v", getErr)
	}
	if stored.Status != "approved" || stored.Version != approved.Version {
		t.Fatalf("blocked directive must stay approved, got status=%s version=%d", stored.Status, stored.Version)
	}
	gate, _ := repository.NewGateUnitRepository(db).GetByCode(context.Background(), "GU-TEST")
	if gate.Status != "closed" {
		t.Fatalf("gate must not move when blocked, got %s", gate.Status)
	}
}

func TestCloseDirectiveStillPassesWhenAboveUpper(t *testing.T) {
	svc, db := newDirectiveService(t)
	approved := approveDirective(t, svc, "OD-ABOVE-UPPER-CLOSE", "closed")
	updateTestReservoir(t, db, "warning", 176.2, ptrFloat(165.0), ptrFloat(175.0))

	executing, err := svc.Transition(context.Background(), approved.ID, dto.TransitionRequest{
		Status: "executing", ExpectedVersion: approved.Version, Reason: "越上限关闸照旧放行",
	}, "operator", model.RoleOperator, "req-execute-close")
	if err != nil {
		t.Fatalf("close directive must still pass above upper level: %v", err)
	}
	if executing.Status != "executing" {
		t.Fatalf("expected executing, got %s", executing.Status)
	}
}

func TestExecuteBlockedBelowLowerStaysApproved(t *testing.T) {
	svc, db := newDirectiveService(t)
	approved := approveDirective(t, svc, "OD-BELOW-LOWER", "open")
	updateTestReservoir(t, db, "warning", 164.8, ptrFloat(165.0), ptrFloat(175.0))

	_, err := svc.Transition(context.Background(), approved.ID, dto.TransitionRequest{
		Status: "executing", ExpectedVersion: approved.Version, Reason: "低于下限开闸应被挡住",
	}, "operator", model.RoleOperator, "req-execute-low")
	if !errors.Is(err, ErrWaterLevelBlocked) {
		t.Fatalf("below-lower open should be blocked, got %v", err)
	}
	if !strings.Contains(err.Error(), "164.80") || !strings.Contains(err.Error(), "下限 165.00") {
		t.Fatalf("block reason must state lower bound and level: %v", err)
	}
}

func TestRestrictedReservoirRejectsOpenDirective(t *testing.T) {
	svc, db := newDirectiveService(t)
	approved := approveDirective(t, svc, "OD-RESTRICTED", "open")
	updateTestReservoir(t, db, "restricted", 170.0, ptrFloat(165.0), ptrFloat(175.0))

	_, err := svc.Transition(context.Background(), approved.ID, dto.TransitionRequest{
		Status: "executing", ExpectedVersion: approved.Version, Reason: "受限库区开闸必须拒绝",
	}, "operator", model.RoleOperator, "req-execute-restricted")
	if !errors.Is(err, ErrWaterLevelBlocked) {
		t.Fatalf("restricted open should be blocked, got %v", err)
	}
	if !strings.Contains(err.Error(), "受限") {
		t.Fatalf("block reason must mention restricted state: %v", err)
	}
}

func TestRestrictedReservoirAllowsCloseDirective(t *testing.T) {
	svc, db := newDirectiveService(t)
	approved := approveDirective(t, svc, "OD-RESTRICTED-CLOSE", "closed")
	updateTestReservoir(t, db, "restricted", 176.8, ptrFloat(165.0), ptrFloat(175.0))

	if _, err := svc.Transition(context.Background(), approved.ID, dto.TransitionRequest{
		Status: "executing", ExpectedVersion: approved.Version, Reason: "受限库区关闸放行",
	}, "operator", model.RoleOperator, "req-execute-restricted-close"); err != nil {
		t.Fatalf("restricted close must pass: %v", err)
	}
}

func TestDirectiveWithoutWaterLevelRangeFollowsCurrentBehavior(t *testing.T) {
	svc, db := newDirectiveService(t)
	approved := approveDirective(t, svc, "OD-NO-RANGE", "open")
	// 区间两端都不填：即便水位在任意位置也按现状放行。
	updateTestReservoir(t, db, "critical", 999.0, nil, nil)

	if _, err := svc.Transition(context.Background(), approved.ID, dto.TransitionRequest{
		Status: "executing", ExpectedVersion: approved.Version, Reason: "无区间按现状放行",
	}, "operator", model.RoleOperator, "req-execute-norange"); err != nil {
		t.Fatalf("directive without range should pass: %v", err)
	}
}

func TestDirectiveListEnrichesReservoirWaterLevelAndPermit(t *testing.T) {
	svc, db := newDirectiveService(t)
	approved := approveDirective(t, svc, "OD-LIST", "open")
	updateTestReservoir(t, db, "warning", 176.2, ptrFloat(165.0), ptrFloat(175.0))

	page, err := svc.List(context.Background(), dto.PageQuery{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("list directives: %v", err)
	}
	var listed *model.OperationDirective
	for index := range page.Items {
		if page.Items[index].ID == approved.ID {
			listed = &page.Items[index]
		}
	}
	if listed == nil {
		t.Fatal("approved directive not found in list")
	}
	if listed.ReservoirCode != "R-TEST" || listed.ReservoirWaterLevel != 176.2 || !listed.HasWaterLevelRange {
		t.Fatalf("list row missing reservoir water level enrichment: %#v", listed)
	}
	if listed.Permitted || !strings.Contains(listed.PermitReason, "176.20") {
		t.Fatalf("list row should show blocked permit with reason, got permitted=%v reason=%q", listed.Permitted, listed.PermitReason)
	}
}

func ptrFloat(value float64) *float64 { return &value }
