package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/benfiola/access-operator/internal"
	"github.com/benfiola/access-operator/internal/operator"
	"github.com/benfiola/access-operator/internal/server"
	"github.com/urfave/cli/v2"
)

// Configures logging for the application.
// Accepts a logging level 'error' | 'warn' | 'info' | 'debug'
func configureLogging(ls string) (*slog.Logger, error) {
	if ls == "" {
		ls = "info"
	}
	var l slog.Level
	switch ls {
	case "error":
		l = slog.LevelError
	case "warn":
		l = slog.LevelWarn
	case "info":
		l = slog.LevelInfo
	case "debug":
		l = slog.LevelDebug
	default:
		return nil, fmt.Errorf("unrecognized log level %s", ls)
	}

	opts := &slog.HandlerOptions{
		Level: l,
	}
	handler := slog.NewTextHandler(os.Stderr, opts)
	logger := slog.New(handler)
	return logger, nil
}

// Used as a key to the urfave/cli context to store the application-level logger.
type ContextLogger struct{}

func main() {
	err := (&cli.App{
		Before: func(c *cli.Context) error {
			logger, err := configureLogging(c.String("log-level"))
			if err != nil {
				return err
			}
			c.Context = context.WithValue(c.Context, ContextLogger{}, logger)
			return nil
		},
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "log-level",
				Usage:   "logging verbosity level",
				EnvVars: []string{"ACCESS_OPERATOR_LOG_LEVEL"},
			},
		},
		Commands: []*cli.Command{
			{
				Name:  "operator",
				Usage: "start operator",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "cloudflare-token",
						Usage:   "cloudflare api token",
						EnvVars: []string{"ACCESS_OPERATOR_CLOUDFLARE_TOKEN"},
					},
					&cli.StringFlag{
						Name:    "kubeconfig",
						Usage:   "path to kubeconfig file",
						EnvVars: []string{"ACCESS_OPERATOR_KUBECONFIG"},
					},
					&cli.StringFlag{
						Name:    "health-bind-address",
						Usage:   "bind address for health/ready probes",
						EnvVars: []string{"ACCESS_OPERATOR_HEALTH_BIND_ADDRESS"},
					},
					&cli.StringFlag{
						Name:    "routeros-address",
						Usage:   "address to routeros device",
						EnvVars: []string{"ACCESS_OPERATOR_ROUTEROS_ADDRESS"},
					},
					&cli.StringFlag{
						Name:    "routeros-password",
						Usage:   "password to routeros device",
						EnvVars: []string{"ACCESS_OPERATOR_ROUTEROS_PASSWORD"},
					},
					&cli.StringFlag{
						Name:    "routeros-username",
						Usage:   "username to routeros device",
						EnvVars: []string{"ACCESS_OPERATOR_ROUTEROS_USERNAME"},
					},
				},
				Action: func(c *cli.Context) error {
					l, ok := c.Context.Value(ContextLogger{}).(*slog.Logger)
					if !ok {
						return fmt.Errorf("logger not attached to context")
					}

					s, err := operator.New(&operator.Opts{
						CloudflareToken:   c.String("cloudflare-token"),
						Kubeconfig:        c.String("kube-config"),
						HealthBindAddress: c.String("health-bind-address"),
						Logger:            l,
						RouterOsAddress:   c.String("routeros-address"),
						RouterOsPassword:  c.String("routeros-password"),
						RouterOsUsername:  c.String("routeros-username"),
					})
					if err != nil {
						return err
					}

					return s.Run(c.Context)
				},
			},
			{
				Name:  "server",
				Usage: "start server",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "bind-address",
						Usage:   "bind address for server",
						EnvVars: []string{"ACCESS_OPERATOR_HEALTH_BIND_ADDRESS"},
					},
					&cli.StringFlag{
						Name:    "kube-config",
						Usage:   "path to kubeconfig file",
						EnvVars: []string{"ACCESS_OPERATOR_KUBECONFIG"},
					},
				},
				Action: func(c *cli.Context) error {
					l, ok := c.Context.Value(ContextLogger{}).(*slog.Logger)
					if !ok {
						return fmt.Errorf("logger not attached to context")
					}

					s, err := server.New(&server.Opts{
						BindAddress: c.String("bind-address"),
						Kubeconfig:  c.String("kube-config"),
						Logger:      l,
					})
					if err != nil {
						return err
					}

					return s.Run(c.Context)
				},
			},
			{
				Name:  "version",
				Usage: "prints the operator version",
				Action: func(c *cli.Context) error {
					fmt.Fprintf(c.App.Writer, "%s", internal.GetVersion())
					return nil
				},
			},
		},
	}).Run(os.Args)
	code := 0
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err.Error())
		code = 1
	}
	os.Exit(code)
}
