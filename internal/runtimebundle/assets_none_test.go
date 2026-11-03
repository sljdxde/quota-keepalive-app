//go:build !embeddedruntime

package runtimebundle

import (
	"errors"
	"testing"
)

func TestUnembeddedBuildReportsUnavailable(t *testing.T) {
	if Available() {
		t.Fatal("未加 embeddedruntime 标签的构建不应内嵌运行时")
	}
	if _, err := Ensure(t.TempDir(), "1.0.0"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Ensure 错误 = %v，want ErrUnavailable", err)
	}
}
