package app

import (
	"testing"

	"qka/internal/selfupdate"
	"qka/internal/settings"
)

func TestApplyUpdateMirrorFollowsSetting(t *testing.T) {
	svc := selfupdate.New("0.1.70", t.TempDir())
	st := settings.Default()
	applyUpdateMirror(svc, st)
	if svc.MirrorPrefix != selfupdate.DomesticMirror {
		t.Fatalf("默认应开启国内镜像: %q", svc.MirrorPrefix)
	}
	if err := st.Set("update_domestic_mirror", false); err != nil {
		t.Fatal(err)
	}
	applyUpdateMirror(svc, st)
	if svc.MirrorPrefix != "" {
		t.Fatalf("关闭后镜像前缀 = %q", svc.MirrorPrefix)
	}
	if err := st.Set("update_domestic_mirror", true); err != nil {
		t.Fatal(err)
	}
	applyUpdateMirror(svc, st)
	if svc.MirrorPrefix != selfupdate.DomesticMirror {
		t.Fatalf("重新开启后镜像前缀 = %q", svc.MirrorPrefix)
	}
}
