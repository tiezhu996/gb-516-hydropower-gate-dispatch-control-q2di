package service

import (
	"fmt"

	"github.com/blueship581/hydropower-gate-dispatch-control/backend/internal/constants"
	"github.com/blueship581/hydropower-gate-dispatch-control/backend/internal/model"
)

// 水位许可放行评估。规则（需求口径）：
//
//   - 库区没填许可区间（下限/上限缺任一端）：按现状放行。
//   - 库区处于 restricted（受限）状态：开闸一律拒绝；关闸不受此条限制。
//   - 指令目标为开闸，且当前水位越过上限（>上限）或掉到下限以下（<下限）：
//     拒绝放行，指令留在已复核。
//   - 水位越上限时的关闸指令：照旧放行（泄洪保坝优先）。
//
// 评估在指令从 approved 推进到 executing 时按所属库区的当前水位判一次。
// 返回的 permitted 为 false 时，blocked 是挡住指令的区间描述，
// waterLevel 是参与判定的当前水位（米），reason 说清挡住它的是哪段区间和哪个水位。

// directiveOpensGate 判断指令目标闸态是否属于开闸动作。
func directiveOpensGate(directive model.OperationDirective) bool {
	return directive.GateState == string(constants.GateStateOpen)
}

// WaterLevelPermit 是一次放行评估的结果。
type WaterLevelPermit struct {
	Permitted bool
	// BlockedBy 标识拦截来源：restricted / above-upper / below-lower / ""。
	BlockedBy string
	Reason    string
}

func formatLevel(value float64) string {
	return fmt.Sprintf("%.2f", value)
}

// evaluateWaterLevelPermit 按库区当前状态和水位评估一条指令能否放行执行。
func evaluateWaterLevelPermit(reservoir model.Reservoir, directive model.OperationDirective) WaterLevelPermit {
	opensGate := directiveOpensGate(directive)

	if reservoir.Status == "restricted" && opensGate {
		return WaterLevelPermit{
			Permitted: false,
			BlockedBy: "restricted",
			Reason: fmt.Sprintf("库区 %s 处于受限状态，开闸一律拒绝（当前水位 %s 米）",
				reservoir.Code, formatLevel(reservoir.WaterLevel)),
		}
	}

	if !reservoir.HasWaterLevelRange() {
		// 库区没填许可区间，按现状放行。
		return WaterLevelPermit{Permitted: true}
	}

	lower := *reservoir.WaterLevelLower
	upper := *reservoir.WaterLevelUpper
	window := fmt.Sprintf("%.2f 米 ~ %.2f 米", lower, upper)

	if opensGate {
		switch {
		case reservoir.WaterLevel > upper:
			return WaterLevelPermit{
				Permitted: false,
				BlockedBy: "above-upper",
				Reason: fmt.Sprintf("库区 %s 当前水位 %s 米，已越过上限 %s 米，放行区间[%s]，禁止开闸",
					reservoir.Code, formatLevel(reservoir.WaterLevel), formatLevel(upper), window),
			}
		case reservoir.WaterLevel < lower:
			return WaterLevelPermit{
				Permitted: false,
				BlockedBy: "below-lower",
				Reason: fmt.Sprintf("库区 %s 当前水位 %s 米，掉到下限 %s 米以下，放行区间[%s]，禁止开闸",
					reservoir.Code, formatLevel(reservoir.WaterLevel), formatLevel(lower), window),
			}
		}
	}

	// 关闸指令不受水位区间限制；水位越上限关闸照旧放行。
	return WaterLevelPermit{Permitted: true}
}
