package dto

import "time"

// CreateReservoir is the public write contract for 库区. Status is deliberately
// omitted so callers cannot bypass the service state machine.
//
// WaterLevelLower/WaterLevelUpper 是开闸许可区间（米），指针用于区分“没填”
// 与 0；两端必须同时为空或同时填写，且下限 < 上限。
type CreateReservoir struct {
	Code            string    `json:"code" binding:"required,min=2,max=64"`
	Name            string    `json:"name" binding:"required,min=2,max=160"`
	Description     string    `json:"description" binding:"max=1000"`
	Facility        string    `json:"facility" binding:"required,max=120"`
	Owner           string    `json:"owner" binding:"required,max=120"`
	Category        string    `json:"category" binding:"required,max=80"`
	RiskLevel       string    `json:"riskLevel" binding:"required,oneof=low medium high critical"`
	MetricValue     float64   `json:"metricValue"`
	MetricUnit      string    `json:"metricUnit" binding:"max=24"`
	EffectiveAt     time.Time `json:"effectiveAt" binding:"required"`
	Evidence        string    `json:"evidence" binding:"max=2000"`
	RelatedCode     string    `json:"relatedCode" binding:"max=64"`
	WaterLevel      float64   `json:"waterLevel"`
	WaterLevelLower *float64  `json:"waterLevelLower"`
	WaterLevelUpper *float64  `json:"waterLevelUpper"`
}

type UpdateReservoir struct {
	ExpectedVersion uint      `json:"expectedVersion" binding:"required"`
	Name            string    `json:"name" binding:"required,min=2,max=160"`
	Description     string    `json:"description" binding:"max=1000"`
	Facility        string    `json:"facility" binding:"required,max=120"`
	Owner           string    `json:"owner" binding:"required,max=120"`
	Category        string    `json:"category" binding:"required,max=80"`
	RiskLevel       string    `json:"riskLevel" binding:"required,oneof=low medium high critical"`
	MetricValue     float64   `json:"metricValue"`
	MetricUnit      string    `json:"metricUnit" binding:"max=24"`
	EffectiveAt     time.Time `json:"effectiveAt" binding:"required"`
	Evidence        string    `json:"evidence" binding:"max=2000"`
	RelatedCode     string    `json:"relatedCode" binding:"max=64"`
	WaterLevel      float64   `json:"waterLevel"`
	WaterLevelLower *float64  `json:"waterLevelLower"`
	WaterLevelUpper *float64  `json:"waterLevelUpper"`
}
