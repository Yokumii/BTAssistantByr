package topfree

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
	"github.com/Yokumii/BTAssistantByr/internal/port"
)

type writeAction struct {
	operation string
	hashes    []domain.TorrentHash
	confirmed bool
}

type notificationBonus struct {
	observedAt time.Time
	a          float64
	seeds      int
	perHour    float64
}

type notificationSnapshot struct {
	space *domain.DiskSpace
	bonus *notificationBonus
}

// SetNotifier enables summaries after attempted writes, inside the approval gate.
func (e *Executor) SetNotifier(notifier port.Notifier, bonus port.BonusTracker, timeout time.Duration) error {
	if notifier == nil || bonus == nil || timeout <= 0 {
		return errors.New("execution notifications require a notifier, bonus source and positive timeout")
	}
	e.notifier, e.notificationBonus, e.notificationTimeout = notifier, bonus, timeout
	return nil
}

func (p *executionProgress) write(operation string, hashes []domain.TorrentHash, run func() error) error {
	action := writeAction{operation: operation, hashes: append([]domain.TorrentHash(nil), hashes...)}
	err := run()
	action.confirmed = err == nil
	if action.confirmed {
		p.confirmedWrites++
	}
	p.actions = append(p.actions, action)
	return err
}

func (e *Executor) observeBeforeWrites(ctx context.Context, progress *executionProgress) {
	ctx, cancel := context.WithTimeout(ctx, e.notificationTimeout)
	defer cancel()
	// Download safety checks already supplied the most recent pre-write reading.
	if progress.before.space == nil {
		progress.before.space = e.notificationSpace(ctx)
	}
	progress.before.bonus = e.notificationBonusSnapshot(ctx)
}

func (e *Executor) notificationSpace(ctx context.Context) *domain.DiskSpace {
	space, err := e.client.FreeSpace(ctx, e.savePath)
	if err == nil {
		err = validateDiskSpace(space)
	}
	if err != nil {
		if e.logger != nil {
			e.logger.WarnContext(ctx, "cannot read notification disk space", "error", err)
		}
		return nil
	}
	return &space
}

func (e *Executor) notificationBonusSnapshot(ctx context.Context) *notificationBonus {
	observedAt := e.now()
	account, err := e.notificationBonus.BonusAccount(ctx)
	if err == nil && (math.IsNaN(account.A) || math.IsInf(account.A, 0) || account.A < 0) {
		err = fmt.Errorf("%w: invalid notification bonus A", port.ErrInvalidTrackerSnapshot)
	}
	var seeded []domain.SeededTorrent
	if err == nil {
		seeded, err = e.notificationBonus.ListSeeding(ctx, account.UserID)
	}
	if err != nil {
		if e.logger != nil {
			e.logger.WarnContext(ctx, "cannot read notification bonus snapshot", "error", err)
		}
		return nil
	}
	return &notificationBonus{observedAt: observedAt, a: account.A, seeds: len(seeded), perHour: bonusPerHour(account.A, len(seeded))}
}

func (e *Executor) notifyExecution(ctx context.Context, plan Plan, progress *executionProgress, executionErr error) {
	if e.notifier == nil || len(progress.actions) == 0 {
		return
	}
	finished := e.now()
	// Shutdown may cancel execution after a delete. Still attempt its summary,
	// with separate bounded budgets for observations and delivery.
	observationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), e.notificationTimeout)
	after := notificationSnapshot{}
	after.space = e.notificationSpace(observationCtx)
	after.bonus = e.notificationBonusSnapshot(observationCtx)
	cancel()
	subject, body := executionEmail(plan, progress, after, finished, executionErr)
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), e.notificationTimeout)
	defer cancel()
	if err := e.notifier.Notify(sendCtx, subject, body); err != nil {
		if e.logger != nil {
			e.logger.ErrorContext(sendCtx, "execution notification failed; writes will not be replayed",
				"phase", progress.phase, "confirmed_write_requests", progress.confirmedWrites, "error", err)
		}
		return
	}
	if e.logger != nil {
		e.logger.InfoContext(sendCtx, "execution notification accepted by email provider", "confirmed_write_requests", progress.confirmedWrites)
	}
}

func executionEmail(plan Plan, progress *executionProgress, after notificationSnapshot, finished time.Time, executionErr error) (string, string) {
	status := "执行完成"
	if executionErr != nil {
		status = "执行中止"
	}
	var body strings.Builder
	fmt.Fprintf(&body, "BTAssistantByr %s\n开始：%s\n结束：%s\n阶段：%s\n已确认成功的写请求：%d\n\n",
		status, progress.started.Format(time.RFC3339), finished.Format(time.RFC3339), progress.phase, progress.confirmedWrites)
	if executionErr != nil {
		body.WriteString("本轮未完整执行。以下仅列出已尝试的请求；未列出的计划动作尚未执行。具体错误见本地日志。\n")
	}
	body.WriteString("写请求明细（按调用顺序）：\n")
	for i, action := range progress.actions {
		result := "请求返回成功"
		if !action.confirmed {
			result = "请求未确认成功，远端可能已有副作用"
		}
		fmt.Fprintf(&body, "%d. %s：%s\n", i+1, action.operation, result)
		for _, hash := range action.hashes {
			fmt.Fprintf(&body, "   Hash=%s\n", hash)
		}
	}
	body.WriteString("\n本轮涉及的下载/删除对象（是否实际执行以上方请求明细为准）：\n")
	for _, torrent := range plan.RemoveExpired {
		writeNotificationTorrent(&body, "失去 TopFree 的未完成任务", torrent.Hash, torrent.Name, torrent.SizeBytes)
	}
	for _, download := range plan.Downloads {
		writeNotificationTorrent(&body, "计划新增", download.Candidate.Hash, download.Candidate.Name, download.Candidate.SizeBytes)
		for _, torrent := range download.DeleteBefore {
			writeNotificationTorrent(&body, "计划回收", torrent.Hash, torrent.Name, torrent.SizeBytes)
		}
	}
	body.WriteString("\nqBittorrent 下载路径真实可用空间：\n")
	writeNotificationSpace(&body, "写操作前", progress.before.space)
	if progress.afterCleanup != nil {
		writeNotificationSpace(&body, "清理后、添加前（最后一次安全复检）", progress.afterCleanup)
	}
	writeNotificationSpace(&body, "执行结束后", after.space)
	if before, end := progress.before.space, after.space; before != nil && end != nil {
		if sameDiskPath(before.Path, end.Path) {
			fmt.Fprintf(&body, "本轮实测可用空间变化：%+.3f GiB\n", float64(end.FreeBytes-before.FreeBytes)/(1<<30))
		} else {
			body.WriteString("路径已变化，无法比较空间差值。\n")
		}
	}
	body.WriteString("空间是各读取时点的实测值；删除文件可能异步完成，其他任务也可能占用或释放空间。Add 返回成功不表示下载已完成。\n")
	body.WriteString("\n站点收益快照（由网页 A 和完整做种数按 BYR 公式换算）：\n")
	writeNotificationBonus(&body, "写操作前", progress.before.bonus)
	writeNotificationBonus(&body, "执行结束后", after.bonus)
	if before, end := progress.before.bonus, after.bonus; before != nil && end != nil {
		fmt.Fprintf(&body, "站点 A 变化：%+.4f；每小时收益变化：%+.4f 魔力/小时\n", end.a-before.a, end.perHour-before.perHour)
	}
	body.WriteString("站点统计可能延迟更新；以上变化不能直接归因于本轮操作。此处未采集账户魔力余额，也不代表实际到账收益。\n")
	if estimate := plan.Bonus; estimate != nil {
		fmt.Fprintf(&body, "\n整轮计划的魔力估算（假设新增任务全部下载完成）：\n快照：%s；有效期至：%s\n网页 A：%.4f；重算 A：%.4f → %.4f\n新增 A：%.4f；删除损失 A：%.4f\n收益：%.4f → %.4f 魔力/小时；净变化：%+.4f 魔力/小时\n",
			estimate.ObservedAt.Format(time.RFC3339), estimate.ValidUntil.Format(time.RFC3339),
			estimate.ReportedA, estimate.BeforeA, estimate.AfterA, estimate.AddedA, estimate.RemovedA,
			estimate.BeforePerHour, estimate.AfterPerHour, estimate.NetPerHour)
		body.WriteString("这是原批准计划的启发式预测；执行中止时不代表已完成部分的收益，也不代表真实到账或全局最优。\n")
	} else {
		body.WriteString("未启用 bonus 优先策略，本轮不提供完整替换计划的收益预测。\n")
	}
	return "[BTAssistantByr] " + status, body.String()
}

func writeNotificationTorrent(body *strings.Builder, label string, hash domain.TorrentHash, name string, size int64) {
	// Quote untrusted names so newlines and terminal control bytes stay visible.
	fmt.Fprintf(body, "%s：name=%q Hash=%s size=%.3f GiB\n", label, name, hash, float64(size)/(1<<30))
}

func writeNotificationSpace(body *strings.Builder, label string, space *domain.DiskSpace) {
	if space == nil {
		fmt.Fprintf(body, "%s：读取失败，数据不可用\n", label)
		return
	}
	fmt.Fprintf(body, "%s：path=%q free=%.3f GiB (%d bytes)\n", label, space.Path, float64(space.FreeBytes)/(1<<30), space.FreeBytes)
}

func writeNotificationBonus(body *strings.Builder, label string, bonus *notificationBonus) {
	if bonus == nil {
		fmt.Fprintf(body, "%s：读取失败，数据不可用\n", label)
		return
	}
	fmt.Fprintf(body, "%s：time=%s A=%.4f 做种数=%d 收益=%.4f 魔力/小时\n", label, bonus.observedAt.Format(time.RFC3339), bonus.a, bonus.seeds, bonus.perHour)
}
