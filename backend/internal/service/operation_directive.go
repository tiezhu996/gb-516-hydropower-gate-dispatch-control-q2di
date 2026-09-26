package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueship581/hydropower-gate-dispatch-control/backend/internal/constants"
	"github.com/blueship581/hydropower-gate-dispatch-control/backend/internal/dto"
	"github.com/blueship581/hydropower-gate-dispatch-control/backend/internal/model"
	"github.com/blueship581/hydropower-gate-dispatch-control/backend/internal/repository"
	"gorm.io/gorm"
)

type OperationDirectiveService interface {
	List(context.Context, dto.PageQuery) (repository.Page[model.OperationDirective], error)
	Get(context.Context, uint) (model.OperationDirective, error)
	Create(context.Context, dto.CreateOperationDirective, string, string) (model.OperationDirective, error)
	Update(context.Context, uint, dto.UpdateOperationDirective, string, string) (model.OperationDirective, error)
	Transition(context.Context, uint, dto.TransitionRequest, string, string, string) (model.OperationDirective, error)
	Delete(context.Context, uint, string, string) error
	StatusCounts(context.Context) (map[string]int64, error)
}

type operationDirectiveService struct {
	repository repository.OperationDirectiveRepository
	gates      repository.GateUnitRepository
	reservoirs repository.ReservoirRepository
	security   SecurityService
}

func NewOperationDirectiveService(repo repository.OperationDirectiveRepository, gates repository.GateUnitRepository, reservoirs repository.ReservoirRepository, security SecurityService) OperationDirectiveService {
	return &operationDirectiveService{repository: repo, gates: gates, reservoirs: reservoirs, security: security}
}

func (s *operationDirectiveService) List(ctx context.Context, query dto.PageQuery) (repository.Page[model.OperationDirective], error) {
	page, err := s.repository.List(ctx, query)
	if err != nil {
		return page, err
	}
	for index := range page.Items {
		s.decorateExecutionContext(ctx, &page.Items[index])
	}
	return page, nil
}

func (s *operationDirectiveService) Get(ctx context.Context, id uint) (model.OperationDirective, error) {
	item, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.OperationDirective{}, err
	}
	s.decorateExecutionContext(ctx, &item)
	return item, nil
}

func (s *operationDirectiveService) Create(ctx context.Context, input dto.CreateOperationDirective, actor, requestID string) (model.OperationDirective, error) {
	if err := validateOperationDirectiveBusinessFields(input.Code, input.Name, input.Facility, input.Owner); err != nil {
		return model.OperationDirective{}, err
	}
	gate, err := s.gates.GetByCode(ctx, input.RelatedCode)
	if err != nil {
		return model.OperationDirective{}, fmt.Errorf("linked gate %q: %w", input.RelatedCode, err)
	}
	if !strings.EqualFold(strings.TrimSpace(input.Facility), gate.Facility) || gate.Status == string(constants.GateStateLocked) {
		return model.OperationDirective{}, fmt.Errorf("%w: linked gate is locked or belongs to another facility", ErrInvalidInput)
	}
	gateState := strings.TrimSpace(input.GateState)
	if gateState == "" {
		gateState = string(constants.GateStateClosed)
	}
	item := model.OperationDirective{
		BaseModel: model.BaseModel{
			Code: strings.ToUpper(strings.TrimSpace(input.Code)), Name: strings.TrimSpace(input.Name),
			Status: model.OperationDirectiveInitialStatus, Version: 1, Description: strings.TrimSpace(input.Description),
		},
		Facility: strings.TrimSpace(input.Facility), Owner: strings.TrimSpace(input.Owner),
		Category: strings.TrimSpace(input.Category), RiskLevel: input.RiskLevel,
		MetricValue: input.MetricValue, MetricUnit: strings.TrimSpace(input.MetricUnit),
		EffectiveAt: input.EffectiveAt.UTC(), Evidence: strings.TrimSpace(input.Evidence),
		RelatedCode: strings.ToUpper(strings.TrimSpace(input.RelatedCode)), GateState: gateState,
	}
	if err := s.security.WithinTransaction(ctx, func(txCtx context.Context) error {
		if err := s.repository.Create(txCtx, &item); err != nil {
			return err
		}
		return s.security.Audit(txCtx, actor, requestID, "create", "OperationDirective", item.ID, "", item.Status, "created 操作指令")
	}); err != nil {
		return model.OperationDirective{}, fmt.Errorf("create 操作指令: %w", err)
	}
	return item, nil
}

func (s *operationDirectiveService) Update(ctx context.Context, id uint, input dto.UpdateOperationDirective, actor, requestID string) (model.OperationDirective, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.OperationDirective{}, err
	}
	if current.Status != string(constants.DirectiveStateDraft) {
		return model.OperationDirective{}, ErrImmutableState
	}
	if err := validateOperationDirectiveBusinessFields(current.Code, input.Name, input.Facility, input.Owner); err != nil {
		return model.OperationDirective{}, err
	}
	gate, err := s.gates.GetByCode(ctx, input.RelatedCode)
	if err != nil {
		return model.OperationDirective{}, fmt.Errorf("linked gate %q: %w", input.RelatedCode, err)
	}
	if !strings.EqualFold(strings.TrimSpace(input.Facility), gate.Facility) || gate.Status == string(constants.GateStateLocked) {
		return model.OperationDirective{}, fmt.Errorf("%w: linked gate is locked or belongs to another facility", ErrInvalidInput)
	}
	current.Name = strings.TrimSpace(input.Name)
	current.Description = strings.TrimSpace(input.Description)
	current.Facility = strings.TrimSpace(input.Facility)
	current.Owner = strings.TrimSpace(input.Owner)
	current.Category = strings.TrimSpace(input.Category)
	current.RiskLevel = input.RiskLevel
	current.MetricValue = input.MetricValue
	current.MetricUnit = strings.TrimSpace(input.MetricUnit)
	current.EffectiveAt = input.EffectiveAt.UTC()
	current.Evidence = strings.TrimSpace(input.Evidence)
	current.RelatedCode = strings.ToUpper(strings.TrimSpace(input.RelatedCode))
	if gateState := strings.TrimSpace(input.GateState); gateState != "" {
		current.GateState = gateState
	}
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = time.Now().UTC()
	if err := s.security.WithinTransaction(ctx, func(txCtx context.Context) error {
		if err := s.repository.Update(txCtx, id, input.ExpectedVersion, &current); err != nil {
			return err
		}
		return s.security.Audit(txCtx, actor, requestID, "update", "OperationDirective", id, current.Status, current.Status, "updated business fields")
	}); err != nil {
		return model.OperationDirective{}, fmt.Errorf("update 操作指令: %w", err)
	}
	return s.Get(ctx, id)
}

func (s *operationDirectiveService) Transition(ctx context.Context, id uint, input dto.TransitionRequest, actor, role, requestID string) (model.OperationDirective, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.OperationDirective{}, err
	}
	target := strings.TrimSpace(input.Status)
	if !constants.CanTransition(constants.OperationDirectiveTransitions, current.Status, target) {
		return model.OperationDirective{}, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, current.Status, target)
	}
	if target == string(constants.DirectiveStateCompleted) {
		return model.OperationDirective{}, fmt.Errorf("%w: completion is recorded by an execution confirmation", ErrInvalidTransition)
	}
	if !directiveRoleAllowed(current.Status, target, role) {
		return model.OperationDirective{}, ErrForbidden
	}
	var gate *model.GateUnit
	var gateTarget string
	if target == string(constants.DirectiveStateExecuting) || (current.Status == string(constants.DirectiveStateExecuting) && target == string(constants.DirectiveStateAborted)) {
		linkedGate, gateErr := s.gates.GetByCode(ctx, current.RelatedCode)
		if gateErr != nil {
			return model.OperationDirective{}, fmt.Errorf("linked gate %q: %w", current.RelatedCode, gateErr)
		}
		if target == string(constants.DirectiveStateExecuting) && linkedGate.Status == string(constants.GateStateLocked) {
			return model.OperationDirective{}, fmt.Errorf("%w: locked gate cannot execute a directive", ErrInvalidInput)
		}
		if target == string(constants.DirectiveStateExecuting) {
			blockReason, permitted, permErr := s.executionPermission(ctx, current, linkedGate)
			if permErr != nil {
				return model.OperationDirective{}, permErr
			}
			if !permitted {
				return model.OperationDirective{}, fmt.Errorf("%w: %s，指令保持已复核", ErrExecutionBlocked, blockReason)
			}
		}
		gate = &linkedGate
		if target == string(constants.DirectiveStateAborted) {
			gateTarget = string(constants.GateStateLocked)
		} else if linkedGate.Status != current.GateState {
			gateTarget = string(constants.GateStateMoving)
		}
		if gateTarget != "" && linkedGate.Status != gateTarget && !constants.CanTransition(constants.GateUnitTransitions, linkedGate.Status, gateTarget) {
			return model.OperationDirective{}, fmt.Errorf("%w: gate %s cannot move from %s to %s", ErrInvalidTransition, linkedGate.Code, linkedGate.Status, gateTarget)
		}
	}
	if target == string(constants.DirectiveStateApproved) {
		if current.SubmittedBy == "" || strings.EqualFold(current.SubmittedBy, actor) {
			return model.OperationDirective{}, ErrTwoPersonRequired
		}
	}
	before := current.Status
	now := time.Now().UTC()
	current.Status = target
	var approval *model.DirectiveApproval
	if target == string(constants.DirectiveStatePending) {
		current.SubmittedBy = actor
		current.SubmittedAt = &now
		approval = &model.DirectiveApproval{Stage: "submitted", Actor: actor, Role: role, RequestID: requestID,
			Reason: strings.TrimSpace(input.Reason), FromState: before, ToState: target, CreatedAt: now}
	}
	if target == string(constants.DirectiveStateApproved) {
		current.ApprovedBy = actor
		current.ApprovedAt = &now
		approval = &model.DirectiveApproval{Stage: "approved", Actor: actor, Role: role, RequestID: requestID,
			Reason: strings.TrimSpace(input.Reason), FromState: before, ToState: target, CreatedAt: now}
	}
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = now
	audit := &model.AuditLog{Actor: actor, RequestID: requestID, Action: "transition", EntityType: "OperationDirective", EntityID: id,
		BeforeState: before, AfterState: target, Detail: input.Reason, CreatedAt: now}
	if err := s.security.WithinTransaction(ctx, func(txCtx context.Context) error {
		if err := s.repository.TransitionWithApproval(txCtx, id, input.ExpectedVersion, &current, approval, audit); err != nil {
			return err
		}
		if gate == nil || gateTarget == "" || gate.Status == gateTarget {
			return nil
		}
		gateBefore := gate.Status
		gate.Status = gateTarget
		gate.Version++
		gate.UpdatedAt = now
		if err := s.gates.Update(txCtx, gate.ID, gate.Version-1, gate); err != nil {
			return err
		}
		return s.security.Audit(txCtx, actor, requestID, "directive_execution", "GateUnit", gate.ID, gateBefore, gateTarget, input.Reason)
	}); err != nil {
		return model.OperationDirective{}, fmt.Errorf("transition 操作指令: %w", err)
	}
	return s.Get(ctx, id)
}

func (s *operationDirectiveService) Delete(ctx context.Context, id uint, actor, requestID string) error {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return err
	}
	if current.Status != string(constants.DirectiveStateDraft) {
		return ErrImmutableState
	}
	return s.security.WithinTransaction(ctx, func(txCtx context.Context) error {
		if err := s.repository.Delete(txCtx, id); err != nil {
			return err
		}
		return s.security.Audit(txCtx, actor, requestID, "delete", "OperationDirective", id, current.Status, "deleted", "soft deleted 操作指令")
	})
}

func directiveRoleAllowed(from, target, role string) bool {
	switch target {
	case string(constants.DirectiveStatePending):
		return role == model.RoleOperator || role == model.RoleAdmin
	case string(constants.DirectiveStateApproved):
		return role == model.RoleReviewer || role == model.RoleAdmin
	case string(constants.DirectiveStateExecuting):
		return role == model.RoleOperator || role == model.RoleAdmin
	case string(constants.DirectiveStateAborted):
		return role == model.RoleOperator || role == model.RoleReviewer || role == model.RoleAdmin
	default:
		return false
	}
}

func (s *operationDirectiveService) StatusCounts(ctx context.Context) (map[string]int64, error) {
	return s.repository.CountByStatus(ctx)
}

func validateOperationDirectiveBusinessFields(code, name, facility, owner string) error {
	if strings.TrimSpace(code) == "" || strings.TrimSpace(name) == "" || strings.TrimSpace(facility) == "" || strings.TrimSpace(owner) == "" {
		return ErrInvalidInput
	}
	return nil
}

// executionPermission 在指令从已复核推进到执行前，按关联闸门所属库区的当前
// 水位判定是否放行。闸门未关联库区或库区未填许可区间时按现状放行。
func (s *operationDirectiveService) executionPermission(ctx context.Context, directive model.OperationDirective, gate model.GateUnit) (string, bool, error) {
	reservoir, err := s.reservoirs.GetByCode(ctx, gate.RelatedCode)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", true, nil
		}
		return "", false, fmt.Errorf("load linked reservoir %q: %w", gate.RelatedCode, err)
	}
	reason, permitted := evaluateExecutionPermission(directive.GateState, reservoir)
	return reason, permitted, nil
}

// evaluateExecutionPermission 是水位许可区间的纯判定逻辑，供执行前校验和列表
// 展示共用：库区受限时开闸一律拒绝；当前水位越过许可上限或低于许可下限时拒绝，
// 但水位越上限时关闸照旧放行。
func evaluateExecutionPermission(gateState string, reservoir model.Reservoir) (string, bool) {
	target := strings.TrimSpace(gateState)
	unit := strings.TrimSpace(reservoir.MetricUnit)
	if unit == "" {
		unit = "m"
	}
	level := reservoir.MetricValue
	if reservoir.Status == "restricted" && target == string(constants.GateStateOpen) {
		return fmt.Sprintf("库区 %s 处于受限状态，开闸一律拒绝（当前水位 %.2f %s）", reservoir.Code, level, unit), false
	}
	if reservoir.WaterLevelMax != nil && level > *reservoir.WaterLevelMax && target != string(constants.GateStateClosed) {
		return fmt.Sprintf("当前水位 %.2f %s 越过库区 %s 许可上限 %.2f %s", level, unit, reservoir.Code, *reservoir.WaterLevelMax, unit), false
	}
	if reservoir.WaterLevelMin != nil && level < *reservoir.WaterLevelMin {
		return fmt.Sprintf("当前水位 %.2f %s 低于库区 %s 许可下限 %.2f %s", level, unit, reservoir.Code, *reservoir.WaterLevelMin, unit), false
	}
	return "", true
}

// decorateExecutionContext 为列表和详情补上所属库区的当前水位以及此刻推进
// 执行是否放行，任何关联数据缺失都保持字段为空而不影响读取。
func (s *operationDirectiveService) decorateExecutionContext(ctx context.Context, item *model.OperationDirective) {
	gate, err := s.gates.GetByCode(ctx, item.RelatedCode)
	if err != nil {
		return
	}
	reservoir, err := s.reservoirs.GetByCode(ctx, gate.RelatedCode)
	if err != nil {
		return
	}
	item.ReservoirCode = reservoir.Code
	level := reservoir.MetricValue
	item.ReservoirWaterLevel = &level
	item.ReservoirWaterUnit = reservoir.MetricUnit
	reason, permitted := evaluateExecutionPermission(item.GateState, reservoir)
	item.ExecutionPermitted = &permitted
	item.ExecutionBlockReason = reason
}
