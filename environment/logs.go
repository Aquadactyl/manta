package environment

import "context"

// ReadLogs uses request cancellation when the environment supports it, while
// retaining compatibility with integrations implementing the existing interface.
func ReadLogs(ctx context.Context, process ProcessEnvironment, lines int) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if reader, ok := process.(interface {
		ReadlogContext(context.Context, int) ([]string, error)
	}); ok {
		return reader.ReadlogContext(ctx, lines)
	}
	return process.Readlog(lines)
}
