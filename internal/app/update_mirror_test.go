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
	if svc.MirrorPrefix != "" {
		t.Fatalf("默认不应开启镜像: %q", svc.MirrorPrefix)
	}
	if err := st.Set("update_domestic_mirror", true); err != nil {
		t.Fatal(err)
	}
	applyUpdateMirror(svc, st)
	if svc.MirrorPrefix != selfupdate.DomesticMirror {
		t.Fatalf("镜像前缀 = %q", svc.MirrorPrefix)
	}
	if err := st.Set("update_domestic_mirror", false); err != nil {
		t.Fatal(err)
	}
	applyUpdateMirror(svc, st)
	if svc.MirrorPrefix != "" {
		t.Fatalf("关闭后镜像前缀 = %q", svc.MirrorPrefix)
	}
}
