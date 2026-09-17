package dockerx

import "testing"

func TestJSONUnmarshalContainer(t *testing.T) {
	line := `{"ID":"abc123","Names":"web","Image":"nginx:latest","State":"running","Status":"Up 2 hours","Ports":"0.0.0.0:80->80/tcp","CreatedAt":"2026-09-18"}`
	var c Container
	if err := jsonUnmarshal(line, &c); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if c.ID != "abc123" || c.Names != "web" || c.Image != "nginx:latest" || c.State != "running" {
		t.Errorf("字段解析不符: %+v", c)
	}
}
