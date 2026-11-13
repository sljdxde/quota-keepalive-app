package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"qka/internal/schedule"
	"qka/internal/scheduler"
)

type fakePlanScheduler struct {
	installed bool
	installs  int
	lastSpec  scheduler.Spec
	pruned    int
	pruneErr  error
}

func (f *fakePlanScheduler) Install(_ context.Context, spec scheduler.Spec) error {
	f.installs++
	f.lastSpec = spec
	f.installed = true
	return nil
}

func (f *fakePlanScheduler) Remove(context.Context, scheduler.Spec) error {
	f.installed = false
	return nil
}

func (f *fakePlanScheduler) Status(context.Context, scheduler.Spec) (scheduler.Status, error) {
	return scheduler.Status{Installed: f.installed}, nil
}

func (f *fakePlanScheduler) PruneOrphans(context.Context, scheduler.Spec) (int, error) {
	return f.pruned, f.pruneErr
}

func planManager(t *testing.T, dir string, fake *fakePlanScheduler) *schedule.Manager {
	t.Helper()
	return &schedule.Manager{
		ConfigDir:  dir,
		Executable: "/Applications/QKA.app/Contents/MacOS/qka",
		Scheduler:  fake,
		Now:        time.Now,
	}
}

// 首次启动（没有 schedule.json）安装默认计划；已有配置不再被覆盖。
func TestEnsureSchedulePlanFirstRunAndPreserveUserChoice(t *testing.T) {
	dir := t.TempDir()
	fake := &fakePlanScheduler{pruned: 2}
	if err := ensureSchedulePlan(context.Background(), planManager(t, dir, fake), dir); err != nil {
		t.Fatal(err)
	}
	cfg, err := schedule.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled || cfg.IntervalMinutes != schedule.DefaultIntervalMinutes || cfg.Action != schedule.ActionAll {
		t.Fatalf("首次启动应写入默认计划，实际 %+v", cfg)
	}
	if fake.installs != 1 {
		t.Fatalf("首次启动应安装 1 次，实际 %d", fake.installs)
	}

	// 用户改成 30 分钟、仅签到并关闭；再次启动必须原样保留。
	user := schedule.Config{Enabled: false, IntervalMinutes: 30, Action: schedule.ActionCheckin, Timezone: schedule.TimezoneLocal}
	if err := schedule.Save(dir, user); err != nil {
		t.Fatal(err)
	}
	fake2 := &fakePlanScheduler{}
	if err := ensureSchedulePlan(context.Background(), planManager(t, dir, fake2), dir); err != nil {
		t.Fatal(err)
	}
	got, err := schedule.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled || got.IntervalMinutes != 30 || got.Action != schedule.ActionCheckin {
		t.Fatalf("启动不应覆盖用户设置，实际 %+v", got)
	}
	if fake2.installs != 0 {
		t.Fatalf("用户关闭后不应重装，实际安装 %d 次", fake2.installs)
	}
}

// 已启用但系统任务缺失（例如重装/被清理）时按现有计划修复，不改 interval/action。
func TestEnsureSchedulePlanRepairsMissingInstall(t *testing.T) {
	dir := t.TempDir()
	cfg := schedule.Config{Enabled: true, IntervalMinutes: 60, Action: schedule.ActionActivity, Timezone: schedule.TimezoneLocal}
	if err := schedule.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	fake := &fakePlanScheduler{installed: false}
	if err := ensureSchedulePlan(context.Background(), planManager(t, dir, fake), dir); err != nil {
		t.Fatal(err)
	}
	if fake.installs != 1 {
		t.Fatalf("任务缺失时应重装 1 次，实际 %d", fake.installs)
	}
	if fake.lastSpec.IntervalMinutes != 60 || fake.lastSpec.ConfigDir != dir {
		t.Fatalf("修复应沿用现有计划，实际 %+v", fake.lastSpec)
	}
}

// 已启用且任务在：不做任何系统写操作；清理失败的陈旧任务不影响启动。
func TestEnsureSchedulePlanIdempotentAndPruneFailureIsSoft(t *testing.T) {
	dir := t.TempDir()
	if err := schedule.Save(dir, schedule.Config{
		Enabled: true, IntervalMinutes: 120, Action: schedule.ActionAll, Timezone: schedule.TimezoneLocal,
	}); err != nil {
		t.Fatal(err)
	}
	fake := &fakePlanScheduler{installed: true, pruned: 0, pruneErr: errors.New("launchctl unavailable")}
	if err := ensureSchedulePlan(context.Background(), planManager(t, dir, fake), dir); err != nil {
		t.Fatalf("清理失败不应让启动失败：%v", err)
	}
	if fake.installs != 0 {
		t.Fatalf("任务已安装时不应重装，实际 %d", fake.installs)
	}
	if err := ensureSchedulePlan(context.Background(), nil, dir); err == nil {
		t.Fatal("缺少管理器应报错")
	}
}

func TestScheduleExists(t *testing.T) {
	dir := t.TempDir()
	if schedule.Exists(dir) {
		t.Fatal("空目录不应被视为已配置")
	}
	if err := schedule.Save(dir, schedule.Default()); err != nil {
		t.Fatal(err)
	}
	if !schedule.Exists(dir) {
		t.Fatal("保存后应被识别为已配置")
	}
}
