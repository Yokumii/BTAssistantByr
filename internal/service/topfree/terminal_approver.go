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

// Approve prints all destructive actions and accepts only an exact APPLY response.
func (a *TerminalApprover) Approve(ctx context.Context, plan Plan) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if _, err := io.WriteString(a.writer, approvalSummary(plan)); err != nil {
		return false, fmt.Errorf("write approval prompt: %w", err)
	}

	line, err := a.reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("read approval response: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return strings.TrimSpace(line) == ApprovalToken, nil
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
	fmt.Fprintf(&summary, "- 删除种子及文件：%d\n", deleteCount)
	fmt.Fprintf(&summary, "- 下载种子：%d\n", len(plan.Downloads))
	fmt.Fprintf(&summary, "- 预计磁盘占用：%.2f GiB\n", bytesAsGiB(plan.ProjectedDiskUsageBytes))
	for _, download := range plan.Downloads {
		for _, torrent := range download.DeleteBefore {
			fmt.Fprintf(&summary, "  删除 hash=%s name=%q size=%.2f GiB\n", torrent.Hash, torrent.Name, bytesAsGiB(torrent.SizeBytes))
		}
		fmt.Fprintf(&summary, "  下载 id=%s name=%q size=%.2f GiB\n", download.Candidate.ID, download.Candidate.Name, bytesAsGiB(download.Candidate.SizeBytes))
	}
	fmt.Fprintf(&summary, "输入 %s 批准本轮全部写操作，其他输入取消：", ApprovalToken)
	return summary.String()
}

func bytesAsGiB(value int64) float64 {
	return float64(value) / float64(int64(1)<<30)
}
