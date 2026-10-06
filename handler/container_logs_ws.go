package handler

// container_logs_ws.go 容器日志实时流（容器深化计划 R4，对标 Dozzle 观感）：
// 浏览器 --WebSocket--> 本桥 --Docker Engine API logs(follow)--> 容器 stdout/stderr。
//
// 与 container_terminal.go（exec TTY 劫持）的关键差异：
//   - logs 端点不需要 101 Upgrade，是普通 200 + chunked 响应体，ReadResponse 后直接读 Body
//     （net/http 自动解 chunked 帧）；
//   - 非 TTY 容器（未开 -t）的 stdout/stderr 双开日志流走 stdcopy 多路复用帧
//     [8 字节头][payload]，头 = 流类型(1=stdout,2=stderr) + 3 零字节 + 4 字节大端长度；
//     TTY 容器（-t）无帧头，是裸字节流——连接前 inspect Config.Tty 决定走哪条路。
//
// 服务端做行重组：payload 可能半行，按流缓冲至 \n 再发整行 JSON 帧，浏览器免 UTF-8 劈裂问题。

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os/exec"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jiuzhao/vmops/service/console"
)

// maxLogLine 单行日志缓冲上限：无换行的巨量输出（如二进制日志）防内存膨胀，超出即截断发送。
const maxLogLine = 1 << 20

// maxLogFrame stdcopy 单帧 payload 上限：头声明的长度超过此值视为脏数据（防恶意长度触发巨分配）。
const maxLogFrame = 16 << 20

// ContainerLogsHandler 容器日志实时流处理器（无状态，可直接构造）。
type ContainerLogsHandler struct{}

// NewContainerLogsHandler 创建容器日志流处理器。
func NewContainerLogsHandler() *ContainerLogsHandler { return &ContainerLogsHandler{} }

// Connect 处理容器日志 WebSocket 连接（GET /api/docker/containers/:id/logs/ws?tail=&timestamps=）。
// 挂载在 docker 路由组（NonViewerMiddleware），viewer 在组上即被 403。
//
// 消息协议：
//
//	服务端 → 客户端：
//	  {"type":"ready","container":...} 流就绪
//	  {"type":"log","stream":"stdout|stderr","data":"一行文本"}
//	  {"type":"eof"} 容器停止/删除，流自然关闭
//	  {"type":"error","msg":"固定中文文案"} 致命错误，随后关闭
//	客户端 → 服务端：{"type":"ping"} → {"type":"pong"}
func (h *ContainerLogsHandler) Connect(c *gin.Context) {
	rawConn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	conn := console.NewConn(rawConn)
	defer conn.Close()

	id := c.Param("id")
	if !safeDockerID(id) {
		_ = conn.WriteJSON(gin.H{"type": "error", "msg": "容器 ID 非法"})
		return
	}
	tail, _ := strconv.Atoi(c.DefaultQuery("tail", "500"))
	if tail <= 0 || tail > 5000 {
		tail = 500
	}
	timestamps := c.Query("timestamps") == "1"

	tty, err := dockerContainerTty(id)
	if err != nil {
		log.Printf("[docker-logs] 容器状态查询失败 container=%s from=%s err=%v", id, c.ClientIP(), err)
		_ = conn.WriteJSON(gin.H{"type": "error", "msg": "容器不存在或 docker 不可用"})
		return
	}

	body, closer, err := dockerContainerLogsStream(id, tail, timestamps)
	if err != nil {
		log.Printf("[docker-logs] 打开日志流失败 container=%s from=%s err=%v", id, c.ClientIP(), err)
		_ = conn.WriteJSON(gin.H{"type": "error", "msg": "日志流打开失败（确认容器存在）"})
		return
	}
	defer closer.Close()

	_ = conn.WriteJSON(gin.H{"type": "ready", "container": id, "tty": tty})
	log.Printf("[docker-logs] WS 已连接 from=%s container=%s tty=%v tail=%d", c.ClientIP(), id, tty, tail)

	var closeOnce sync.Once
	stop := func() { closeOnce.Do(func() { _ = closer.Close() }) }

	// 日志流 → WS（自起协程必须自兜底 recover，项目规范 10）
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("[docker-logs] 转发 panic container=%s panic=%v\n%s", id, rec, debug.Stack())
			}
			_ = conn.WriteJSON(gin.H{"type": "eof"})
			stop()
		}()
		pipeLogsToWS(conn, body, tty, id)
	}()

	// WS → 服务端：仅心跳；读错误（浏览器关闭）即收工
	for {
		_, data, rerr := conn.ReadMessage()
		if rerr != nil {
			break
		}
		var f struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(data, &f) == nil && f.Type == "ping" {
			_ = conn.WriteJSON(gin.H{"type": "pong"})
		}
	}
	stop()
}

// pipeLogsToWS 从日志流读字节、解复用（或 TTY 直通）、按行重组后发 WS 帧。
func pipeLogsToWS(conn *console.Conn, body io.Reader, tty bool, id string) {
	buf := make([]byte, 8192)
	// 每流一个行缓冲（stdout/stderr 各自成行，避免交叉拼接）
	lineBuf := map[byte][]byte{1: {}, 2: {}}
	var carry []byte // 解复用未消费的残余（跨 Read 的半帧）

	emit := func(stream byte, data []byte) {
		lineBuf[stream] = append(lineBuf[stream], data...)
		for {
			b := lineBuf[stream]
			idx := indexByte(b, '\n')
			if idx < 0 {
				break
			}
			line := b[:idx]
			lineBuf[stream] = b[idx+1:]
			sendLogLine(conn, stream, line)
		}
		// 超长行护栏：无换行时截断发送，防止撑爆内存与前端 DOM
		if len(lineBuf[stream]) > maxLogLine {
			sendLogLine(conn, stream, lineBuf[stream])
			lineBuf[stream] = lineBuf[stream][:0]
		}
	}

	for {
		n, rerr := body.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if tty {
				emit(1, chunk) // TTY 容器无帧头，全部按 stdout（daemon 侧已合流）
			} else {
				carry = append(carry, chunk...)
				for {
					stream, payload, rest, res := demuxStep(carry)
					if res == demuxNeedMore {
						carry = rest
						break
					}
					if res == demuxInvalid {
						// 头非法：视为裸流降级直通（防某版本 daemon 变体导致整条流不可用）
						log.Printf("[docker-logs] 解复用头非法，降级直通 container=%s", id)
						emit(1, carry)
						carry = nil
						break
					}
					emit(stream, payload)
					carry = rest
				}
			}
		}
		if rerr != nil {
			// 流结束：冲刷各流残留半行
			for stream, b := range lineBuf {
				if len(b) > 0 {
					sendLogLine(conn, stream, b)
				}
			}
			return
		}
	}
}

// sendLogLine 发送一行日志帧（stdout/stderr 分色）。
func sendLogLine(conn *console.Conn, stream byte, line []byte) {
	kind := "stdout"
	if stream == 2 {
		kind = "stderr"
	}
	_ = conn.WriteJSON(gin.H{"type": "log", "stream": kind, "data": string(line)})
}

func indexByte(b []byte, c byte) int {
	for i := 0; i < len(b); i++ {
		if b[i] == c {
			return i
		}
	}
	return -1
}

// demuxStatus 解复用单步结果。
type demuxStatus int

const (
	demuxOK       demuxStatus = iota // 成功解析一帧
	demuxNeedMore                    // 缓冲不足一帧，需继续读
	demuxInvalid                     // 头非法（非 stdcopy 帧）
)

// demuxStep 从 stdcopy 缓冲解析一帧：头 8 字节 = 流类型(1=stdout,2=stderr) + 3 零字节 + 4 字节大端长度。
// 返回解析出的流类型、payload 与剩余缓冲。纯函数，便于单测（半帧/边界/脏头/超长帧）。
func demuxStep(buf []byte) (stream byte, payload, rest []byte, res demuxStatus) {
	if len(buf) < 8 {
		return 0, nil, buf, demuxNeedMore
	}
	if buf[0] != 1 && buf[0] != 2 {
		return 0, nil, buf, demuxInvalid
	}
	if buf[1] != 0 || buf[2] != 0 || buf[3] != 0 {
		return 0, nil, buf, demuxInvalid
	}
	size := binary.BigEndian.Uint32(buf[4:8])
	if size > maxLogFrame {
		return 0, nil, buf, demuxInvalid
	}
	if len(buf) < 8+int(size) {
		return 0, nil, buf, demuxNeedMore
	}
	return buf[0], buf[8 : 8+int(size)], buf[8+int(size):], demuxOK
}

// dockerContainerTty 查询容器是否以 TTY 模式创建（决定日志流是否含 stdcopy 帧头）。
func dockerContainerTty(id string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{.Config.Tty}}", id).Output()
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) == "true", nil
}

// dockerContainerLogsStream 打开容器日志 follow 流（GET /v{ver}/containers/{id}/logs）。
// 返回响应体（net/http 已解 chunked）与关闭器；长连接撤销请求期超时。
func dockerContainerLogsStream(id string, tail int, timestamps bool) (io.Reader, io.Closer, error) {
	conn, err := net.Dial("unix", dockerSockPath)
	if err != nil {
		return nil, nil, fmt.Errorf("连接 docker.sock: %w", err)
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	path := fmt.Sprintf("http://docker/v%s/containers/%s/logs?follow=1&stdout=1&stderr=1&tail=%d",
		dockerAPIVersion(), id, tail)
	if timestamps {
		path += "&timestamps=1"
	}
	req, err := http.NewRequest(http.MethodGet, path, nil)
	if err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("构造 logs 请求: %w", err)
	}
	if werr := req.Write(conn); werr != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("发送 logs 请求: %w", werr)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("读取 logs 响应: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = conn.Close()
		return nil, nil, fmt.Errorf("logs 流打开失败: HTTP %d %s", resp.StatusCode, string(respBody))
	}
	_ = conn.SetDeadline(time.Time{}) // follow 为长连接，撤销请求期超时
	return resp.Body, conn, nil
}
