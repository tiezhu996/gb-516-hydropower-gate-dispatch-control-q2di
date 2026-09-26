package model

import "time"

// Reservoir models 库区 as an independently versioned aggregate. The fields
// cover ownership, operational context, evidence and measured risk so later
// changes naturally span persistence, service and UI layers.
type Reservoir struct {
	BaseModel
	Facility    string    `json:"facility" gorm:"size:120;index"`
	Owner       string    `json:"owner" gorm:"size:120;index"`
	Category    string    `json:"category" gorm:"size:80;index"`
	RiskLevel   string    `json:"riskLevel" gorm:"size:32;index"`
	MetricValue float64   `json:"metricValue"`
	MetricUnit  string    `json:"metricUnit" gorm:"size:24"`
	EffectiveAt time.Time `json:"effectiveAt"`
	Evidence    string    `json:"evidence" gorm:"size:2000"`
	RelatedCode string    `json:"relatedCode" gorm:"size:64;index"`
	// WaterLevelMin/WaterLevelMax 是水位许可区间的下限和上限，单位米。
	// 两者都为空表示库区未配置许可区间，指令执行按现状放行。
	WaterLevelMin *float64 `json:"waterLevelMin"`
	WaterLevelMax *float64 `json:"waterLevelMax"`
}

func (item *Reservoir) GetBase() *BaseModel { return &item.BaseModel }

func (item Reservoir) TableName() string { return "reservoirs" }

var ReservoirInitialStatus = "normal"
