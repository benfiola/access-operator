package operator

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	v1 "github.com/benfiola/access-operator/pkg/api/bfiola.dev/v1"
	cloudflarego "github.com/cloudflare/cloudflare-go"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// DNSSync is the public interface of the [dnsSync] component
type DNSSync interface {
	Run(ctx context.Context) error
}

// dnsSync is the application component that synchronizes [v1.Access] DNS records with cloudflare.
type dnsSync struct {
	logger       *slog.Logger
	cloudflare   *cloudflarego.API
	kube         client.Client
	syncInterval time.Duration
}

// DNSSyncOpts are options used to construct a new [dnsSync] object.
type DNSSyncOpts struct {
	CloudflareToken string
	Kubeconfig      string
	Logger          *slog.Logger
	SyncInterval    time.Duration
}

// Creates a new [dnsSync] with the provided [DNSSyncOpts].
func NewDNSSync(o *DNSSyncOpts) (*dnsSync, error) {
	l := o.Logger
	if l == nil {
		l = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	si := o.SyncInterval
	if si == 0 {
		si = 15 * time.Second
	}

	c, err := cloudflarego.NewWithAPIToken(o.CloudflareToken)
	if err != nil {
		return nil, err
	}

	rc, err := clientcmd.BuildConfigFromFlags("", o.Kubeconfig)
	if err != nil {
		return nil, err
	}
	s := runtime.NewScheme()
	err = v1.AddToScheme(s)
	if err != nil {
		return nil, err
	}
	k, err := client.New(rc, client.Options{Scheme: s})
	if err != nil {
		return nil, err
	}

	return &dnsSync{
		logger:       l,
		cloudflare:   c,
		kube:         k,
		syncInterval: si,
	}, nil
}

// An error that contains several errors.
// See: [dnsSync.tick]
type errorList struct {
	Errors []error
}

// Implements the [error] interface - rendering an error string.
// If the [ErrorList] contains no errors, returns a specific message.
// If the [ErrorList] contains one error, returns that error's message.
// If the [ErrorList] contains more than one error, returns the error count.
func (el errorList) Error() string {
	switch len(el.Errors) {
	case 0:
		return "no errors (is this a bug?)"
	case 1:
		return el.Errors[0].Error()
	default:
		return fmt.Sprintf("%d errors", len(el.Errors))
	}
}

// Performs a 'tick' of the [dnsSync] run-loop - in which [v1.Access] dns records are sync'ed with cloudflare
// Returns an error if any of the preliminary API requests fail (fetching cloudflare dns records, fetching kubernetes resources)
// Returns an [ErrorList] of records that failed to sync otherwise.
func (ds *dnsSync) tick() error {
	ctx := context.Background()

	// get ip address
	r, err := http.Get("http://whatismyip.akamai.com")
	if err != nil {
		return err
	}
	if r.StatusCode != 200 {
		return fmt.Errorf("request to %s failed with status code %d", r.Request.URL, r.StatusCode)
	}
	ipb, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	ip := string(ipb)

	// get cloudflare zones
	cfzm := map[string]*cloudflarego.ResourceContainer{}
	cfzs, err := ds.cloudflare.ListZones(ctx)
	if err != nil {
		return err
	}
	for _, cfz := range cfzs {
		cfzm[cfz.Name] = cloudflarego.ZoneIdentifier(cfz.ID)
	}

	// collect access dns records
	as := &v1.AccessList{}
	err = ds.kube.List(ctx, as)
	if err != nil {
		return err
	}
	dnss := []string{}
	for _, a := range as.Items {
		if a.Status.CurrentSpec == nil {
			continue
		}
		dnss = append(dnss, a.Status.CurrentSpec.Dns)
	}

	// group access dns records by zone
	azidnsm := map[*cloudflarego.ResourceContainer]map[string]bool{}
	for _, dns := range dnss {
		ps := strings.Split(dns, ".")
		for i := range ps {
			d := strings.Join(ps[i:], ".")
			zi, ok := cfzm[d]
			if !ok {
				continue
			}
			_, ok = azidnsm[zi]
			if !ok {
				azidnsm[zi] = map[string]bool{}
			}
			azidnsm[zi][dns] = true
			break
		}
	}

	errs := []error{}
	// process access operator resources per-zone
	for zi, adnss := range azidnsm {
		// get cloudflare dns records
		cfdnss, _, err := ds.cloudflare.ListDNSRecords(ctx, zi, cloudflarego.ListDNSRecordsParams{})
		if err != nil {
			return err
		}

		// calculate added, updated, deleted records
		adds := map[string]bool{}
		for k, v := range adnss {
			adds[k] = v
		}
		deletes := map[string]string{}
		updates := map[string]string{}
		for _, cfdns := range cfdnss {
			if !strings.HasPrefix(cfdns.Comment, "access-operator") {
				continue
			}
			if _, ok := adnss[cfdns.Name]; !ok {
				deletes[cfdns.Name] = cfdns.ID
				continue
			}
			delete(adds, cfdns.Name)
			if cfdns.Content != ip || (cfdns.Proxied == nil || !*cfdns.Proxied) || cfdns.TTL != 60 || cfdns.Type != "A" {
				updates[cfdns.Name] = cfdns.ID
			}
		}

		// perform changes
		for a := range adds {
			ds.logger.Info(fmt.Sprintf("create %s", a))
			_, err := ds.cloudflare.CreateDNSRecord(ctx, zi, cloudflarego.CreateDNSRecordParams{
				Type:    "A",
				Content: ip,
				Comment: "access-operator",
				Name:    a,
				Proxied: ptr(false),
				TTL:     60,
			})
			if err != nil {
				errs = append(errs, err)
			}
		}
		for d, i := range deletes {
			ds.logger.Info(fmt.Sprintf("delete %s (id: %s)", d, i))
			err := ds.cloudflare.DeleteDNSRecord(ctx, zi, i)
			if err != nil {
				errs = append(errs, err)
			}
		}
		for u, i := range updates {
			ds.logger.Info(fmt.Sprintf("update %s (id: %s)", u, i))
			_, err := ds.cloudflare.UpdateDNSRecord(ctx, zi, cloudflarego.UpdateDNSRecordParams{
				Proxied: ptr(false),
				Content: ip,
				ID:      i,
				TTL:     60,
			})
			if err != nil {
				errs = append(errs, err)
			}
		}
	}

	if len(errs) != 0 {
		return errorList{Errors: errs}
	}

	return nil
}

// Runs the [dnsSync] component in a run loop.
// Returns an error if syncing fails.
func (ds *dnsSync) Run(ctx context.Context) error {
	var err error
	for {
		time.Sleep(ds.syncInterval)
		cancelled := false
		select {
		case <-ctx.Done():
			cancelled = true
		default:
		}
		if cancelled {
			break
		}
		err = ds.tick()
		if err != nil {
			break
		}
	}
	return err
}
