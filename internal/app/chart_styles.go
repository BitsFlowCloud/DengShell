package app

import (
	"errors"
	"math"
)

type ChartLineStyle struct {
	Color  string  `json:"color"` // Empty follows the current theme.
	Width  float64 `json:"width"`
	Dashed bool    `json:"dashed"`
}

func validateChartStyles(styles map[string]ChartLineStyle) error {
	for id, style := range styles {
		if id != "upload" && id != "download" && id != "latency" {
			return errors.New("曲线设置包含未知线条")
		}
		if style.Color != "" && !validTerminalColor(style.Color) {
			return errors.New("曲线颜色应为 #RRGGBB 格式")
		}
		if math.IsNaN(style.Width) || math.IsInf(style.Width, 0) || style.Width < .5 || style.Width > 6 {
			return errors.New("曲线粗细应为 0.5–6 px")
		}
		if id == "latency" && style.Dashed {
			return errors.New("延迟曲线使用实线")
		}
	}
	return nil
}
