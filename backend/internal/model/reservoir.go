package model

import "time"

// Reservoir models 库区 as an independently versioned aggregate. The fields
// cover ownership, operational context, evidence and measured risk so later
// changes naturally span persistence, service and UI layers.
//
// WaterLevel is the 当前库水位（米）。WaterLevelLower/WaterLevelUpper 是开闸
// 许可区间（米），两者必须成对出现；任一端留空表示库区没有配置许可区间，
// 指令推进执行时按现状放行（详见 OperationDirective 的放行评估）。
type Reservoir struct {
	BaseModel
	Facility        string    `json:"facility" gorm:"size:120;index"`
	Owner           string    `json:"owner" gorm:"size:120;index"`
	Category        string    `json:"category" gorm:"size:80;index"`
	RiskLevel       string    `json:"riskLevel" gorm:"size:32;index"`
	MetricValue     float64   `json:"metricValue"`
	MetricUnit      string    `json:"metricUnit" gorm:"size:24"`
	EffectiveAt     time.Time `json:"effectiveAt"`
	Evidence        string    `json:"evidence" gorm:"size:2000"`
	RelatedCode     string    `json:"relatedCode" gorm:"size:64;index"`
	WaterLevel      float64   `json:"waterLevel"`
	WaterLevelLower *float64  `json:"waterLevelLower"`
	WaterLevelUpper *float64  `json:"waterLevelUpper"`
}

func (item *Reservoir) GetBase() *BaseModel { return &item.BaseModel }

func (item Reservoir) TableName() string { return "reservoirs" }

var ReservoirInitialStatus = "normal"

// HasWaterLevelRange 报告库区是否配置了完整的水位许可区间。只有下限和上限
// 成对存在时才算配置了区间；缺任一端等同于没填，按现状放行。
func (item Reservoir) HasWaterLevelRange() bool {
	return item.WaterLevelLower != nil && item.WaterLevelUpper != nil
}
