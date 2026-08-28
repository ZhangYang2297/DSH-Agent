// promql.go —— 对接 Prometheus HTTP API 的轻量客户端（只依赖标准库）
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
)

// Prometheus 地址，可用环境变量覆盖（默认本机 9090）
func promBaseURL() string {
	if v := os.Getenv("PROMETHEUS_URL"); v != "" {
		return v
	}
	return "http://127.0.0.1:9090"
}

// queryRangeResult 对应 /api/v1/query_range 的响应
type queryRangeResult struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Values [][2]any          `json:"values"` // [unix_time, value]
		} `json:"result"`
	} `json:"data"`
}

// queryInstantResult 对应 /api/v1/query 的响应
type queryInstantResult struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]any `json:"metric"`
			Value  [2]any         `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

// queryRange 执行 PromQL range 查询，返回 (指标名→样本序列)
func queryRange(promQL, start, end string, step time.Duration) ([]map[string]any, error) {
	u := fmt.Sprintf("%s/api/v1/query_range", promBaseURL())
	params := url.Values{
		"query": {promQL},
		"start": {start},
		"end":   {end},
		"step":  {strconv.FormatFloat(step.Seconds(), 'f', -1, 64)},
	}
	resp, err := http.Get(u + "?" + params.Encode())
	if err != nil {
		return nil, fmt.Errorf("prometheus query_range 请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("prometheus 返回 %d: %s", resp.StatusCode, string(body))
	}

	var r queryRangeResult
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("解析 query_range 响应失败: %w", err)
	}
	if r.Status != "success" {
		return nil, fmt.Errorf("prometheus status=%s", r.Status)
	}

	out := []map[string]any{}
	for _, item := range r.Data.Result {
		series := make([]map[string]any, 0, len(item.Values))
		for _, v := range item.Values {
			if len(v) != 2 {
				continue
			}
			series = append(series, map[string]any{
				"t": v[0],
				"v": v[1],
			})
		}
		// 采样点数多时做降采样，保留前 60 个点，避免刷爆上下文
		const maxPoints = 60
		if len(series) > maxPoints {
			stepN := len(series) / maxPoints
			down := make([]map[string]any, 0, maxPoints)
			for i := 0; i < len(series); i += stepN {
				down = append(down, series[i])
			}
			series = down
		}
		out = append(out, map[string]any{
			"metric": item.Metric,
			"values": series,
		})
	}
	return out, nil
}

// queryInstant 执行 PromQL instant 查询，返回 (指标名→当前值)
func queryInstant(promQL string) ([]map[string]any, error) {
	u := fmt.Sprintf("%s/api/v1/query", promBaseURL())
	params := url.Values{"query": {promQL}}
	resp, err := http.Get(u + "?" + params.Encode())
	if err != nil {
		return nil, fmt.Errorf("prometheus query 请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("prometheus 返回 %d: %s", resp.StatusCode, string(body))
	}

	var r queryInstantResult
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("解析 query 响应失败: %w", err)
	}
	if r.Status != "success" {
		return nil, fmt.Errorf("prometheus status=%s", r.Status)
	}

	out := []map[string]any{}
	for _, item := range r.Data.Result {
		val := ""
		if len(item.Value) == 2 {
			val = fmt.Sprintf("%v", item.Value[1])
		}
		out = append(out, map[string]any{
			"metric": item.Metric,
			"value":  val,
		})
	}
	return out, nil
}
