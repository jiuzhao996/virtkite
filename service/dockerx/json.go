package dockerx

import "encoding/json"

// jsonUnmarshal 单行 JSON 解析（docker --format '{{json .}}' 输出）。
func jsonUnmarshal(line string, v interface{}) error {
	return json.Unmarshal([]byte(line), v)
}
