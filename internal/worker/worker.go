package worker

import "context"

type Worker struct{}

func New() *Worker {
	return &Worker{}
}

func (w *Worker) Run(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}
