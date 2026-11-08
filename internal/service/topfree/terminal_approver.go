package topfree

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

const ApprovalToken = "APPLY"

var (
	// ErrApprovalReaderRequired indicates that no interactive input was supplied.
	ErrApprovalReaderRequired = errors.New("approval input is required")
	// ErrApprovalWriterRequired indicates that no prompt output was supplied.
	ErrApprovalWriterRequired = errors.New("approval output is required")
)

// TerminalApprover displays a plan and requires the exact approval token on one line.
type TerminalApprover struct {
	reader *bufio.Reader
	writer io.Writer
}

// NewTerminalApprover creates an interactive approver, normally from os.Stdin and os.Stdout.
func NewTerminalApprover(reader io.Reader, writer io.Writer) (*TerminalApprover, error) {
	if reader == nil {
		return nil, ErrApprovalReaderRequired
	}
	if writer == nil {
		return nil, ErrApprovalWriterRequired
	}
	return &TerminalApprover{reader: bufio.NewReader(reader), writer: writer}, nil
}

// Approve prints all planned writes and accepts only an exact APPLY response.
func (a *TerminalApprover) Approve(ctx context.Context, plan Plan) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if _, err := io.WriteString(a.writer, approvalSummary(plan)); err != nil {
		return false, fmt.Errorf("write approval prompt: %w", err)
	}

	response := make(chan approvalResponse, 1)
	go func() {
		line, err := a.reader.ReadString('\n')
		response <- approvalResponse{line: line, err: err}
	}()

	var result approvalResponse
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case result = <-response:
	}
	line, err := result.line, result.err
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("read approval response: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return strings.TrimSpace(line) == ApprovalToken, nil
}

type approvalResponse struct {
	line string
	err  error
}

func approvalSummary(plan Plan) string {
	deleteCount := 0
	for _, download := range plan.Downloads {
		deleteCount += len(download.DeleteBefore)
	}

	var summary strings.Builder
	summary.WriteString("\nTopFree 本轮写操作计划：\n")
	fmt.Fprintf(&summary, "- 添加 sc 标签：%d\n", len(plan.TagAsManaged))
	fmt.Fprintf(&summary, "- 移除 toDel 标签：%d\n", len(plan.UnmarkForDeletion))
	fmt.Fprintf(&summary, "- 添加 toDel 标签：%d\n", len(plan.MarkForDeletion))
	fmt.Fprintf(&summary, "- 失去 TopFree 后立即停止并删除：%d\n", len(plan.RemoveExpired))
	fmt.Fprintf(&summary, "- 为释放空间删除种子及文件：%d\n", deleteCount)
	fmt.Fprintf(&summary, "- 等待队列：%d\n", len(plan.Queued))
	fmt.Fprintf(&summary, "- 下载种子：%d\n", len(plan.Downloads))
	fmt.Fprintf(&summary, "- 预计磁盘占用：%.2f GiB\n", bytesAsGiB(plan.ProjectedDiskUsageBytes))
	if bonus := plan.Bonus; bonus != nil {
		fmt.Fprintf(&summary, "- 魔力快照时间：%s\n", bonus.ObservedAt.Format("2006-01-02 15:04:05Z07:00"))
		fmt.Fprintf(&summary, "- 魔力计划有效至：%s（批准超时需重新规划）\n", bonus.ValidUntil.Format("2006-01-02 15:04:05Z07:00"))
		fmt.Fprintf(&summary, "- 网页 A：%.3f；按当前做种清单估算 A：%.3f → %.3f\n", bonus.ReportedA, bonus.BeforeA, bonus.AfterA)
		fmt.Fprintf(&summary, "- 新增 A：%.3f；删除损失 A：%.3f\n", bonus.AddedA, bonus.RemovedA)
		fmt.Fprintf(&summary, "- 容量替换净增益：%+.3f 魔力/小时（以必需的失效清理后为基线）\n", bonus.ReplacementGainPerHour)
		fmt.Fprintf(&summary, "- 预计魔力/小时：%.3f → %.3f（净变化 %+.3f，假设新种完成下载并持续做种）\n", bonus.BeforePerHour, bonus.AfterPerHour, bonus.NetPerHour)
	}
	for _, hash := range plan.BonusUnavailable {
		fmt.Fprintf(&summary, "  不参与容量回收 hash=%s reason=bonus_metadata_unavailable\n", hash)
	}
	for _, skipped := range plan.Skipped {
		fmt.Fprintf(&summary, "  跳过 id=%s reason=%s\n", skipped.Candidate.ID, skipped.Reason)
	}
	if plan.EffectiveSavePath != "" {
		fmt.Fprintf(&summary, "- 下载路径：%s\n", plan.EffectiveSavePath)
		fmt.Fprintf(&summary, "- 检测可用空间：%.2f GiB\n", bytesAsGiB(plan.InitialFreeSpaceBytes))
		fmt.Fprintf(&summary, "- 下载后保留空间：至少 %.2f GiB（预计 %.2f GiB）\n", bytesAsGiB(plan.MinFreeSpaceBytes), bytesAsGiB(plan.ProjectedFreeSpaceBytes))
	}
	for _, torrent := range plan.RemoveExpired {
		fmt.Fprintf(&summary, "  立即清理 hash=%s name=%q downloaded=%.2f GiB state=%s\n", torrent.Hash, torrent.Name, bytesAsGiB(torrent.CompletedBytes), torrent.State)
	}
	for _, download := range plan.Downloads {
		for _, torrent := range download.DeleteBefore {
			fmt.Fprintf(&summary, "  删除 hash=%s name=%q size=%.2f GiB releasable=%.2f GiB\n", torrent.Hash, torrent.Name, bytesAsGiB(torrent.SizeBytes), bytesAsGiB(torrent.CompletedBytes))
		}
		fmt.Fprintf(&summary, "  下载 id=%s name=%q size=%.2f GiB\n", download.Candidate.ID, download.Candidate.Name, bytesAsGiB(download.Candidate.SizeBytes))
	}
	fmt.Fprintf(&summary, "输入 %s 批准本轮全部写操作，其他输入取消：", ApprovalToken)
	return summary.String()
}

func bytesAsGiB(value int64) float64 {
	return float64(value) / float64(int64(1)<<30)
}
