package handler

import (
	"bytes"
	"encoding/binary"
	"strconv"
	"testing"
)

// frame 构造一个 stdcopy 帧：流类型字节 + 3 零字节 + 4 字节大端长度 + payload。
func frame(stream byte, payload []byte) []byte {
	b := []byte{stream, 0, 0, 0}
	var sz [4]byte
	binary.BigEndian.PutUint32(sz[:], uint32(len(payload)))
	b = append(b, sz[:]...)
	return append(b, payload...)
}

// TestDemuxStepBasic 单帧完整解析。
func TestDemuxStepBasic(t *testing.T) {
	buf := frame(1, []byte("hello\n"))
	stream, payload, rest, res := demuxStep(buf)
	if res != demuxOK {
		t.Fatalf("期望 demuxOK，实际 %v", res)
	}
	if stream != 1 || string(payload) != "hello\n" || len(rest) != 0 {
		t.Fatalf("解析不符: stream=%d payload=%q rest=%d", stream, payload, len(rest))
	}
}

// TestDemuxStepStderr 流类型 2 识别为 stderr。
func TestDemuxStepStderr(t *testing.T) {
	stream, payload, _, res := demuxStep(frame(2, []byte("err")))
	if res != demuxOK || stream != 2 || string(payload) != "err" {
		t.Fatalf("stderr 帧解析不符: stream=%d payload=%q res=%v", stream, payload, res)
	}
}

// TestDemuxStepNeedMore 头不足 8 字节、payload 未到齐均需继续读（跨 Read 半帧）。
func TestDemuxStepNeedMore(t *testing.T) {
	// 头不足
	if _, _, rest, res := demuxStep([]byte{1, 0, 0}); res != demuxNeedMore || len(rest) != 3 {
		t.Fatalf("头不足应 demuxNeedMore: res=%v rest=%d", res, len(rest))
	}
	// 头齐但 payload 未到齐
	partial := frame(1, []byte("abcdef"))[:10] // 头 8 + 2 字节
	if _, _, rest, res := demuxStep(partial); res != demuxNeedMore || len(rest) != 10 {
		t.Fatalf("payload 未齐应 demuxNeedMore: res=%v rest=%d", res, len(rest))
	}
}

// TestDemuxStepInvalid 流类型非 1/2、保留位非零、超长长度均判脏头。
func TestDemuxStepInvalid(t *testing.T) {
	cases := map[string][]byte{
		"流类型非法": {3, 0, 0, 0, 0, 0, 0, 0},
		"保留位非零": {1, 1, 0, 0, 0, 0, 0, 0},
		"超长长度":  {1, 0, 0, 0, 0xff, 0xff, 0xff, 0xff},
	}
	for name, buf := range cases {
		if _, _, _, res := demuxStep(buf); res != demuxInvalid {
			t.Errorf("%s 应 demuxInvalid，实际 %v", name, res)
		}
	}
}

// TestDemuxStepMultiFrame 多帧连续解析 + 半帧残留（模拟跨 Read 拼接）。
func TestDemuxStepMultiFrame(t *testing.T) {
	buf := append(frame(1, []byte("line1\n")), frame(2, []byte("line2\n"))...)
	buf = append(buf, 1, 0, 0, 0) // 第三帧只有半个头

	var got []string
	rest := buf
	for {
		stream, payload, r, res := demuxStep(rest)
		if res == demuxNeedMore {
			rest = r
			break
		}
		if res != demuxOK {
			t.Fatalf("意外结果: %v", res)
		}
		got = append(got, strconv.Itoa(int(stream))+":"+string(payload))
		rest = r
	}
	if len(got) != 2 || got[0] != "1:line1\n" || got[1] != "2:line2\n" {
		t.Fatalf("多帧解析不符: %v", got)
	}
	if !bytes.Equal(rest, []byte{1, 0, 0, 0}) {
		t.Fatalf("残留半帧不符: %v", rest)
	}
}
