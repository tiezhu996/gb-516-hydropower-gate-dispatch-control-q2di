package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/blueship581/hydropower-gate-dispatch-control/backend/internal/config"
	"github.com/blueship581/hydropower-gate-dispatch-control/backend/internal/dto"
	"github.com/blueship581/hydropower-gate-dispatch-control/backend/internal/model"
	"github.com/blueship581/hydropower-gate-dispatch-control/backend/internal/repository"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newReservoirService(t *testing.T) ReservoirService {
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
	if err := db.AutoMigrate(&model.Reservoir{}, &model.AuditLog{}); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	security := NewSecurityService(repository.NewSecurityRepository(db), config.Config{})
	return NewReservoirService(repository.NewReservoirRepository(db), security)
}

func reservoirInput(code string, min, max *float64) dto.CreateReservoir {
	return dto.CreateReservoir{
		Code: code, Name: "水位窗口库区", Description: "验证水位许可区间配置",
		Facility: "右岸坝段", Owner: "运行一组", Category: "常规", RiskLevel: "low",
		MetricValue: 168.5, MetricUnit: "m", EffectiveAt: time.Now().UTC(),
		Evidence: "水位计读数已核对", WaterLevelMin: min, WaterLevelMax: max,
	}
}

func TestReservoirWaterLevelWindowValidation(t *testing.T) {
	service := newReservoirService(t)
	ctx := context.Background()
	invertedMin, invertedMax := 170.0, 165.0
	_, err := service.Create(ctx, reservoirInput("R-INVERTED", &invertedMin, &invertedMax), "operator", "req-inverted")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("inverted window should be rejected, got %v", err)
	}

	windowMin, windowMax := 165.0, 172.0
	created, err := service.Create(ctx, reservoirInput("R-WINDOW", &windowMin, &windowMax), "operator", "req-create")
	if err != nil {
		t.Fatalf("create reservoir with window: %v", err)
	}
	if created.WaterLevelMin == nil || *created.WaterLevelMin != windowMin || created.WaterLevelMax == nil || *created.WaterLevelMax != windowMax {
		t.Fatalf("window not persisted: %#v", created)
	}

	_, err = service.Update(ctx, created.ID, dto.UpdateReservoir{
		ExpectedVersion: created.Version, Name: created.Name, Facility: created.Facility, Owner: created.Owner,
		Category: created.Category, RiskLevel: created.RiskLevel, MetricValue: created.MetricValue, MetricUnit: "m",
		EffectiveAt: created.EffectiveAt, Evidence: created.Evidence, WaterLevelMin: &invertedMin, WaterLevelMax: &invertedMax,
	}, "operator", "req-update-inverted")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("inverted window on update should be rejected, got %v", err)
	}
}

func TestReservoirWithoutWindowIsAllowed(t *testing.T) {
	service := newReservoirService(t)
	created, err := service.Create(context.Background(), reservoirInput("R-OPEN", nil, nil), "operator", "req-open")
	if err != nil {
		t.Fatalf("reservoir without window should be creatable: %v", err)
	}
	if created.WaterLevelMin != nil || created.WaterLevelMax != nil {
		t.Fatalf("window should stay empty: %#v", created)
	}
}
