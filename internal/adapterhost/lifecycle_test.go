package adapterhost

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestAdapterHostHelperProcess 是测试二进制兼任「假适配器进程」的入口。
// 由同包测试用 os.Args[0] 启动，避免依赖真实 bun；正常跑测试时直接跳过。
func TestAdapterHostHelperProcess(t *testing.T) {
	if os.Getenv("QKA_ADAPTER_HOST_HELPER") != "1" {
		t.Skip("仅作为子进程入口，不直接运行")
	}
	switch os.Getenv("QKA_ADAPTER_HOST_MODE") {
	case "hang":
		// 模拟卡死的适配器：启动成功但永不回应 initialize。
		time.Sleep(30 * time.Second)
	case "final-response":
		// 模拟「写完最后一条响应立即退出」：验证宿主不丢最后一行。
		_, _ = os.Stdout.WriteString(`{"jsonrpc":"2.0","id":1,"result":{"ok":true}}` + "\n")
	}
	os.Exit(0)
}

// helperCommand 返回把当前测试二进制当作假适配器启动的命令。
func helperCommand(mode string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=TestAdapterHostHelperProcess")
	cmd.Env = append(os.Environ(),
		"QKA_ADAPTER_HOST_HELPER=1",
		"QKA_ADAPTER_HOST_MODE="+mode,
	)
	return cmd
}

// TestLaunchFailsWhenInitializeHangs 覆盖 H1：initialize 握手必须有超时，
// 卡死的适配器不能拖住调用方（进而拖住整个启动链路）。
func TestLaunchFailsWhenInitializeHangs(t *testing.T) {
	originalCmd := newAdapterCommand
	t.Cleanup(func() { newAdapterCommand = originalCmd })
	newAdapterCommand = func(string, ...string) *exec.Cmd { return helperCommand("hang") }

	originalTimeout := initializeTimeout
	initializeTimeout = 150 * time.Millisecond
	t.Cleanup(func() { initializeTimeout = originalTimeout })

	started := time.Now()
	_, err := LaunchWith(context.Background(), "bun", fakeAdapter, nil)
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("适配器卡死时握手必须超时失败")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("期望超时错误，实际 %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("超时未生效，握手耗时 %v", elapsed)
	}
}

// TestLaunchKeepsFinalResponseBeforeExit 覆盖 M3：子进程写完响应立即退出时，
// 宿主必须先读完 stdout 再 Wait，不能把最后一条响应当「已退出」丢掉。
func TestLaunchKeepsFinalResponseBeforeExit(t *testing.T) {
	originalCmd := newAdapterCommand
	t.Cleanup(func() { newAdapterCommand = originalCmd })
	newAdapterCommand = func(string, ...string) *exec.Cmd { return helperCommand("final-response") }

	// 连跑多轮，降低「最后一刻退出」的时序偶然性。
	for i := 0; i < 20; i++ {
		h, err := LaunchWith(context.Background(), "bun", fakeAdapter, nil)
		if err != nil {
			t.Fatalf("第 %d 轮：最后一条响应在进程退出前写出，握手不应失败: %v", i, err)
		}
		_ = h.Close()
	}
}

// TestWaitLoopWaitsForStdoutDrain 直接验证回收顺序：stdout 未读完前不得 Wait。
func TestWaitLoopWaitsForStdoutDrain(t *testing.T) {
	h := &Host{
		cmd:     exec.Command(os.Args[0], "-test.run=TestNonexistentHelper"),
		stdin:   nopWriteCloser{io.Discard},
		pending: map[int]chan rpcResponse{},
	}
	stdoutDone := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		h.waitLoop(stdoutDone)
	}()

	select {
	case <-finished:
		t.Fatal("stdout 尚未读完时不应回收子进程")
	case <-time.After(100 * time.Millisecond):
	}

	close(stdoutDone)
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("stdout 读完后应完成回收")
	}
	if !h.Exited() {
		t.Fatal("回收后应标记已退出")
	}
}
