package server

import (
	"context"
	"io"
	"log/slog"

	"golang.org/x/sync/errgroup"
	"k8s.io/klog/v2"
)

// Main holds the components of the application
type Main struct {
	Server Server
}

// Opts define the options used to create a new instance of [Main]
type Opts struct {
	BindAddress         string
	IpDiscoveryStrategy string
	Logger              *slog.Logger
	Kubeconfig          string
}

// Creates a new instance of [Main] with the provided [Opts].
func New(o *Opts) (*Main, error) {
	l := o.Logger
	if l == nil {
		l = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	klog.SetSlogLogger(l.With("name", "klog"))

	s, err := NewServer(&ServerOpts{
		BindAddress:         o.BindAddress,
		IpDiscoveryStrategy: o.IpDiscoveryStrategy,
		Kubeconfig:          o.Kubeconfig,
		Logger:              l.With("name", "server"),
	})
	if err != nil {
		return nil, err
	}

	return &Main{
		Server: s,
	}, nil
}

// Runs the application.
// Blocks until one of the components fail with an error
func (m *Main) Run(ctx context.Context) error {
	g, sctx := errgroup.WithContext(ctx)
	g.Go(func() error { return m.Server.Run(sctx) })
	return g.Wait()
}
