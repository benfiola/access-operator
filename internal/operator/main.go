package operator

import (
	"context"
	"io"
	"log/slog"

	"golang.org/x/sync/errgroup"
	"k8s.io/klog/v2"
)

// Main holds the components of the application
type Main struct {
	Operator Operator
	DNSSync  DNSSync
}

// Opts define the options used to create a new instance of [Main]
type Opts struct {
	CloudflareToken   string
	HealthBindAddress string
	Logger            *slog.Logger
	Kubeconfig        string
	RouterOsAddress   string
	RouterOsPassword  string
	RouterOsUsername  string
}

// Creates a new instance of [Main] with the provided [Opts].
func New(o *Opts) (*Main, error) {
	l := o.Logger
	if l == nil {
		l = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	klog.SetSlogLogger(l.With("name", "klog"))

	var d *dnsSync
	var err error
	if o.CloudflareToken != "" {
		d, err = NewDNSSync(&DNSSyncOpts{
			CloudflareToken: o.CloudflareToken,
			Kubeconfig:      o.Kubeconfig,
			Logger:          l.With("name", "dnssync"),
		})
		if err != nil {
			return nil, err
		}
	}

	op, err := NewOperator(&OperatorOpts{
		HealthBindAddress: o.HealthBindAddress,
		Kubeconfig:        o.Kubeconfig,
		Logger:            l.With("name", "operator"),
		RouterOsAddress:   o.RouterOsAddress,
		RouterOsPassword:  o.RouterOsPassword,
		RouterOsUsername:  o.RouterOsUsername,
	})
	if err != nil {
		return nil, err
	}

	return &Main{
		DNSSync:  d,
		Operator: op,
	}, nil
}

// Runs the application.
// Blocks until one of the components fail with an error
func (m *Main) Run(ctx context.Context) error {
	g, _ := errgroup.WithContext(ctx)
	g.Go(func() error { return m.Operator.Run(ctx) })
	if ptr, ok := m.DNSSync.(*dnsSync); ok && ptr != nil {
		g.Go(func() error { return m.DNSSync.Run(ctx) })
	}
	return g.Wait()
}
