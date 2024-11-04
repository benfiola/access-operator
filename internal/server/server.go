package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/benfiola/access-operator/internal"
	v1 "github.com/benfiola/access-operator/pkg/api/bfiola.dev/v1"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	slogecho "github.com/samber/slog-echo"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	IpDiscoveryStrategyProd = "prod"
	IpDiscoveryStrategyDev  = "dev"
)

// Server is the public interface for the server implementation
type Server interface {
	Run(ctx context.Context) error
}

// Internal server data struct that binds a [Provider] to endpoint functions
type server struct {
	bindAddress         string
	echo                *echo.Echo
	ipDiscoveryStrategy string
	kube                client.Client
	logger              *slog.Logger
}

// Health endpoint returning a 200 status code.
func (s *server) health(c echo.Context) error {
	return c.String(http.StatusOK, "ok")
}

// listAppsItem represents an application exposed by a [v1.Access] resource.
// See: [server.listApps]
// See: [server.authorizeApp]
type listAppsItem struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Dns       string `json:"dns"`
}

// +kubebuilder:rbac:groups=bfiola.dev,resources=accesses,verbs=list

// Lists applications that are exposed via a [v1.Access] resource
func (s *server) listApps(c echo.Context) error {
	ctx := context.Background()

	al := &v1.AccessList{}
	err := s.kube.List(ctx, al)
	if err != nil {
		return err
	}

	ais := []listAppsItem{}
	for _, a := range al.Items {
		if a.Status.CurrentSpec == nil {
			continue
		}
		ai := listAppsItem{
			Namespace: a.Namespace,
			Name:      a.Name,
			Dns:       a.Status.CurrentSpec.Dns,
		}
		ais = append(ais, ai)
	}

	return c.JSON(http.StatusOK, ais)
}

// +kubebuilder:rbac:groups=bfiola.dev,resources=accesses,verbs=get;update
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get

// authorizeAppBody is used to store a user-entered password
// See: [server.authorizeApp]
type authorizeAppBody struct {
	Password string `json:"password"`
}

// Authorizes a user to access a [v1.Access] resource
func (s *server) authorizeApp(c echo.Context) error {
	ctx := context.Background()

	// extract data from request
	ns := c.Param("namespace")
	n := c.Param("name")
	b := &authorizeAppBody{}
	err := c.Bind(b)
	if err != nil {
		return c.String(http.StatusBadRequest, "bad request")
	}

	// fetch application
	a := &v1.Access{}
	err = s.kube.Get(ctx, types.NamespacedName{Namespace: ns, Name: n}, a)
	if err != nil {
		return err
	}

	// fetch application secret
	ps := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: a.Namespace, Name: a.Spec.PasswordRef.Name}}
	err = s.kube.Get(ctx, client.ObjectKeyFromObject(ps), ps)
	if err != nil {
		return err
	}
	pb, ok := ps.Data[a.Spec.PasswordRef.Key]
	if !ok {
		return fmt.Errorf("key not found: %s/%s/%s", ps.Namespace, ps.Name, a.Spec.PasswordRef.Key)
	}
	p := string(pb)

	// authorize request
	if p != b.Password {
		return c.NoContent(http.StatusUnauthorized)
	}

	// get ip address
	ip := c.RealIP()
	if ip == "" {
		return c.String(http.StatusBadRequest, "could not determine ip")
	}

	// add member/update existing member expiry on success
	d, err := time.ParseDuration(a.Spec.Ttl)
	if err != nil {
		return err
	}
	m := v1.Member{
		Cidr:    fmt.Sprintf("%s/32", ip),
		Expires: time.Now().Add(d).Format(time.RFC3339),
	}
	f := false
	for i := range a.Spec.Members {
		if m.Cidr != a.Spec.Members[i].Cidr {
			continue
		}
		f = true
		a.Spec.Members[i].Expires = m.Expires
	}
	if !f {
		a.Spec.Members = append(a.Spec.Members, m)
	}
	err = s.kube.Update(ctx, a)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, c.Request().Header)
}

func extractDevIp(r *http.Request) string {
	return "254.254.254.254"
}

func extractCloudflareIp(r *http.Request) string {
	return r.Header.Get("CF-Connecting-IP")
}

// Options provided to [NewServer]
type ServerOpts struct {
	BindAddress         string
	IpDiscoveryStrategy string
	Kubeconfig          string
	Logger              *slog.Logger
	StaticPath          string
}

// Constructs a [server] using the provided options within [ServerOpts]
func NewServer(o *ServerOpts) (*server, error) {
	l := o.Logger
	if l == nil {
		l = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	ba := o.BindAddress
	if ba == "" {
		ba = ":8080"
	}

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	rc, err := clientcmd.BuildConfigFromFlags("", o.Kubeconfig)
	if err != nil {
		return nil, err
	}
	scheme := runtime.NewScheme()
	err = corev1.AddToScheme(scheme)
	if err != nil {
		return nil, err
	}
	err = v1.AddToScheme(scheme)
	if err != nil {
		return nil, err
	}
	k, err := client.New(rc, client.Options{Scheme: scheme})
	if err != nil {
		return nil, err
	}

	ids := o.IpDiscoveryStrategy
	if ids == "" {
		ids = IpDiscoveryStrategyProd
	}
	switch ids {
	case IpDiscoveryStrategyDev:
		e.IPExtractor = extractDevIp
	case IpDiscoveryStrategyProd:
		e.IPExtractor = extractCloudflareIp
	default:
		return nil, fmt.Errorf("unknown ip discovery strategy %s", ids)
	}
	s := server{
		bindAddress:         ba,
		echo:                e,
		ipDiscoveryStrategy: ids,
		kube:                k,
		logger:              l,
	}

	staticFs, err := internal.GetStaticFS()
	if err != nil {
		return nil, err
	}

	e.Use(slogecho.New(l))
	e.Use(middleware.StaticWithConfig(middleware.StaticConfig{
		Browse:     true,
		HTML5:      true,
		Filesystem: http.FS(staticFs),
	}))
	e.GET("/healthz", s.health)
	e.GET("/api/apps/list", s.listApps)
	e.POST("/api/apps/:namespace/:name/authorize", s.authorizeApp)
	return &s, nil
}

// Runs the [server] using its internal configuration
func (s *server) Run(ctx context.Context) error {
	var err error

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.logger.Info(fmt.Sprintf("starting server: %s", s.bindAddress))
		err = s.echo.Start(s.bindAddress)
	}()

	cancelled := false
	for {
		time.Sleep(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			cancelled = true
		default:
		}
		if cancelled {
			s.logger.Info("stopping server")
			s.echo.Shutdown(context.Background())
			break
		}
		if err != nil {
			break
		}
	}

	wg.Wait()
	if err != nil && cancelled {
		if strings.Contains(err.Error(), "http: Server closed") {
			err = nil
		}
	}

	return err
}
