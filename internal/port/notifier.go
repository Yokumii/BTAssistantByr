package port

import "context"

// Notifier delivers one execution summary without replaying the execution.
type Notifier interface {
	Notify(ctx context.Context, subject, text string) error
}
