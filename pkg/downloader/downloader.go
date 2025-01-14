package downloader

import (
	"context"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram/downloader"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/iyear/tdl/pkg/dcpool"
	"github.com/iyear/tdl/pkg/logger"
)

type Downloader struct {
	opts Options
}

type Options struct {
	Pool     dcpool.Pool
	PartSize int
	Threads  int
	Iter     Iter
	Progress Progress
}

func New(opts Options) *Downloader {
	return &Downloader{
		opts: opts,
	}
}

func (d *Downloader) Download(ctx context.Context, limit int) error {
	wg, wgctx := errgroup.WithContext(ctx)
	wg.SetLimit(limit)

	for d.opts.Iter.Next(wgctx) {
		elem := d.opts.Iter.Value()

		wg.Go(func() (rerr error) {
			d.opts.Progress.OnAdd(elem)
			defer func() { d.opts.Progress.OnDone(elem, rerr) }()

			if err := d.download(wgctx, elem); err != nil {
				// canceled by user, so we directly return error to stop all
				if errors.Is(err, context.Canceled) {
					return errors.Wrap(err, "download")
				}

				// don't return error, just log it
			}

			return nil
		})
	}

	if err := d.opts.Iter.Err(); err != nil {
		return errors.Wrap(err, "iter")
	}

	return wg.Wait()
}

func (d *Downloader) download(ctx context.Context, elem Elem) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	logger.From(ctx).Debug("Start download elem",
		zap.Any("elem", elem))

	client := d.opts.Pool.Client(ctx, elem.File().DC())
	if elem.AsTakeout() {
		client = d.opts.Pool.Takeout(ctx, elem.File().DC())
	}

	_, err := downloader.NewDownloader().WithPartSize(d.opts.PartSize).
		Download(client, elem.File().Location()).
		WithThreads(d.bestThreads(elem.File().Size())).
		Parallel(ctx, newWriteAt(elem, d.opts.Progress, d.opts.PartSize))
	if err != nil {
		return errors.Wrap(err, "download")
	}

	return nil
}

// threads level
// TODO(iyear): more practice to find best number
var threads = []struct {
	threads int
	size    int64
}{
	{1, 1 << 20},
	{2, 5 << 20},
	{4, 20 << 20},
	{8, 50 << 20},
}

// Get best threads num for download, based on file size
func (d *Downloader) bestThreads(size int64) int {
	for _, t := range threads {
		if size < t.size {
			return min(t.threads, d.opts.Threads)
		}
	}
	return d.opts.Threads
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
