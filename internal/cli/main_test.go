package cli

import (
	"os"
	"testing"
)

// TestMain 让测试包默认关闭内嵌运行时：带 embeddedruntime 标签构建测试时，
// 解析逻辑不会真去工作目录解压资源，既有用例的行为与普通构建完全一致。
// 需要验证接线优先级的用例自己替换 resolveEmbeddedRuntime。
func TestMain(m *testing.M) {
	runtimeAvailable = func() bool { return false }
	os.Exit(m.Run())
}
