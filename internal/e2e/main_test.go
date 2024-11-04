package e2e

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/benfiola/access-operator/internal/operator"
	v1 "github.com/benfiola/access-operator/pkg/api/bfiola.dev/v1"
	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	"github.com/go-routeros/routeros/v3"
	"github.com/neilotoole/slogt"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	kscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// Adds a test object to a test objects list.
// This list is used to help during between-test cleanup.
// See: [Setup]
func CreateTestObject[T client.Object](v T) T {
	testObjects = append(testObjects, v)
	return v
}

// Defines kubernetes objects referenced during tests.
// Using a static set of tests ensures that cleanup is consistent between test runs.
// The tests themselves should create these objects as needed.
// [Setup] handles the cleanup of these resources.
var (
	testObjects = []client.Object{}
	pod         = CreateTestObject(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "nginx", Labels: map[string]string{"asdf": "nginx"}},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "nginx", Image: "nginx:latest", Ports: []corev1.ContainerPort{{ContainerPort: 80}}}}},
	})
	service = CreateTestObject(&corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "nginx"},
		Spec: corev1.ServiceSpec{
			Ports:    []corev1.ServicePort{{Port: 80}},
			Selector: pod.ObjectMeta.Labels,
		},
	})
	secret = CreateTestObject(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "secret"},
		StringData: map[string]string{"password": "test-password"},
	})

	serviceAccessClaim = CreateTestObject(&v1.AccessClaim{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "test"},
		Spec: v1.AccessClaimSpec{
			Dns:         "testing.example",
			PasswordRef: v1.SecretKeyRef{Name: secret.Name, Key: "password"},
			ServiceTemplates: []corev1.ServiceSpec{{
				Ports:    []corev1.ServicePort{{Port: 81, Protocol: "TCP", TargetPort: intstr.IntOrString{IntVal: 80}}},
				Selector: pod.ObjectMeta.Labels, Type: "LoadBalancer",
			}},
		},
	})
	// NOTE: must match [serviceAccessClaim] to ensure accessClaim testing cleans up this resource indirectly
	serviceAccess = CreateTestObject(&v1.Access{
		ObjectMeta: serviceAccessClaim.ObjectMeta,
		Spec: v1.AccessSpec{
			Dns:              serviceAccessClaim.Spec.Dns,
			PasswordRef:      v1.SecretKeyRef{Name: secret.Name, Key: "password"},
			ServiceTemplates: serviceAccessClaim.Spec.ServiceTemplates,
			Members:          []v1.Member{},
		},
	})

	ingressAccessClaim = CreateTestObject(&v1.AccessClaim{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "test"},
		Spec: v1.AccessClaimSpec{
			Dns: "testing.example",
			IngressTemplate: &networkingv1.IngressSpec{
				DefaultBackend: &networkingv1.IngressBackend{
					Service: &networkingv1.IngressServiceBackend{
						Name: service.ObjectMeta.Name,
						Port: networkingv1.ServiceBackendPort{Number: 80},
					},
				},
			},
			PasswordRef: v1.SecretKeyRef{Name: secret.Name, Key: "password"},
		},
	})
	// NOTE: must match [ingressAccessClaim] to ensure accessClaim testing cleans up this resource indirectly
	ingressAccess = CreateTestObject(&v1.Access{
		ObjectMeta: ingressAccessClaim.ObjectMeta,
		Spec: v1.AccessSpec{
			Dns:             ingressAccessClaim.Spec.Dns,
			IngressTemplate: ingressAccessClaim.Spec.IngressTemplate,
			Members:         []v1.Member{},
			PasswordRef:     v1.SecretKeyRef{Name: secret.Name, Key: "password"},
		},
	})
)

// TestData holds data used during tests
// See [Setup].
type TestData struct {
	Ctx        context.Context
	Kube       client.Client
	Kubeconfig string
	Require    require.Assertions
	Routeros   *routeros.Client
	T          testing.TB
}

// Cleans up existing test objects.
// Returns a set of data used across most (all?) tests.
func Setup(t testing.TB) TestData {
	t.Helper()

	require := require.New(t)
	ctx := context.Background()

	// create kube client
	kc := os.Getenv("KUBECONFIG")
	rcfg, err := clientcmd.BuildConfigFromFlags("", kc)
	require.NoError(err, "build client config")
	s := runtime.NewScheme()
	err = kscheme.AddToScheme(s)
	require.NoError(err, "add kubernetes resources to scheme")
	err = v1.AddToScheme(s)
	require.NoError(err, "add access-operator resources to scheme")
	err = ciliumv2.AddToScheme(s)
	require.NoError(err, "add cilium resources to scheme")
	k, err := client.New(rcfg, client.Options{Scheme: s, Cache: &client.CacheOptions{}})
	require.NoError(err, "build client")

	// delete resources if they exist
	for _, r := range testObjects {
		nr := r.DeepCopyObject().(client.Object)
		for {
			err := k.Get(ctx, client.ObjectKeyFromObject(nr), nr)
			if err != nil && apierrors.IsNotFound(err) {
				break
			}
			require.NoError(err, "fetch existing testing k8s resource")
			nr.SetFinalizers([]string{})
			err = k.Update(ctx, nr)
			if err != nil && apierrors.IsConflict(err) {
				continue
			}
			require.NoError(err, "remove finalizers from testing k8s resource")
			err = k.Delete(ctx, nr, client.PropagationPolicy("Foreground"))
			if err != nil && apierrors.IsNotFound(err) {
				err = nil
			}
			require.NoError(err, "delete testing k8s resource")
			break
		}
		for {
			err := k.Get(ctx, client.ObjectKeyFromObject(nr), nr)
			if err != nil && apierrors.IsNotFound(err) {
				break
			}
			require.NoError(err, "wait for testing k8s resource deletion")
		}
	}

	// create routeros client
	rc, err := routeros.Dial("127.0.0.1:8728", "admin", "")
	require.NoError(err, "create routeros client")
	t.Cleanup(func() { rc.Close() })

	// delete routeros resources
	re, err := rc.Run("/ip/firewall/filter/print", "detail")
	require.NoError(err, "list firewall filters")
	for _, s := range re.Re {
		_, err := rc.Run("/ip/firewall/filter/remove", fmt.Sprintf("=numbers=%s", s.Map[".id"]))
		require.NoError(err, "delete firewall filter")
	}
	re, err = rc.Run("/ip/firewall/address-list/print", "detail")
	require.NoError(err, "list firewall address lists")
	for _, s := range re.Re {
		_, err := rc.Run("/ip/firewall/address-list/remove", fmt.Sprintf("=numbers=%s", s.Map[".id"]))
		require.NoError(err, "delete firewall address list")
	}
	re, err = rc.Run("/ip/firewall/nat/print", "detail")
	require.NoError(err, "list firewall nat")
	for _, s := range re.Re {
		_, err := rc.Run("/ip/firewall/nat/remove", fmt.Sprintf("=numbers=%s", s.Map[".id"]))
		require.NoError(err, "delete firewall nat")
	}

	return TestData{
		Ctx:        context.Background(),
		Kube:       k,
		Kubeconfig: kc,
		Require:    *require,
		Routeros:   rc,
		T:          t,
	}
}

// LogLevelHandler configures a [slog.Handler] with a log level derived from the environment.
type LogLevelHandler struct {
	slog.Handler
}

func (src *LogLevelHandler) GetLevel() slog.Level {
	l := slog.Level(100)
	// os.Setenv("VERBOSE", "1")
	if os.Getenv("VERBOSE") == "1" {
		l = slog.LevelDebug
	}
	return l
}

// Part of the [LogLevelHandler] implementation of [slog.Handler]
func (src *LogLevelHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	return lvl >= src.GetLevel()
}

// Used by [LogLevelHandler.Handle] to prevent error logs from being emitted.
type DoNotLog struct{}

func (e *DoNotLog) Error() string {
	return "do not log"
}

// Part of the [LogLevelHandler] implementation of [slog.Handler]
// NOTE: [logr.Logger] will log error messages regardless of verbosity - which is why this method is required
// NOTE: [DoNotLog] is returned (vs. nil) - otherwise the log-line prefix will be emitted.
func (src *LogLevelHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= src.GetLevel() {
		return src.Handler.Handle(ctx, r)
	}
	return &DoNotLog{}
}

// Part of the [LogLevelHandler] implementation of [slog.Handler]
func (src *LogLevelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &LogLevelHandler{Handler: src.Handler.WithAttrs(attrs)}
}

// Part of the [LogLevelHandler] implementation of [slog.Handler]
func (src *LogLevelHandler) WithGroup(name string) slog.Handler {
	return &LogLevelHandler{Handler: src.Handler.WithGroup(name)}
}

// Returns a [slogt.Option] wrapping a [slogt.Bridge]'s [slog.Handler] in a [LogLevelHandler]
func WithLogLevelHandler() slogt.Option {
	return func(b *slogt.Bridge) {
		b.Handler = &LogLevelHandler{Handler: b.Handler}
	}
}

// Runs operator until provided function returns [StopIteration].
func RunOperatorUntil(td TestData, rof func() error) {
	td.T.Helper()

	// create operator
	l := slogt.New(td.T, slogt.Text(), WithLogLevelHandler())
	o, err := operator.New(&operator.Opts{Kubeconfig: td.Kubeconfig, Logger: l, RouterOsAddress: "127.0.0.1:8728", RouterOsUsername: "admin"})
	td.Require.NoError(err, "create operator")

	// start operator in background
	var oerr error
	sctx, cancel := context.WithCancel(td.Ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		oerr = o.Run(sctx)
	}()

	// poll function until function returns error
	// NOTE: StopIteration is an error
	to := 30 * time.Second
	st := time.Now()
	for {
		ct := time.Now()
		if ct.Sub(st) > to {
			err = fmt.Errorf("timed out waiting for end condition")
			break
		}
		err := rof()
		if err != nil {
			break
		}
	}

	// stop the operator
	cancel()
	wg.Wait()

	// if an error was thrown while polling,
	if err != nil {
		td.Require.ErrorIs(err, StopIteration{}, "within run operator function")
	}
	td.Require.NoError(oerr, "during operator run")
}

// StopIteration signals to [RunOperatorUntil] that it should stop polling kubernetes for state changes.
type StopIteration struct{}

// Returns an error string for [StopIteration]
func (si StopIteration) Error() string {
	return "stop iteration"
}

// Waits for the provided resource to be deleted
func WaitForDelete(td TestData, o client.Object) {
	td.T.Helper()

	RunOperatorUntil(td, func() error {
		err := td.Kube.Get(td.Ctx, client.ObjectKeyFromObject(o), o)
		if err == nil {
			return nil
		}
		if apierrors.IsNotFound(err) {
			err = nil
		}
		td.Require.NoError(err, "fetch object while waiting for object to be deleted")
		return StopIteration{}
	})
}

// Ensures that over a set number of iterations, the operator isn't changing the [client.Object]'s ResourceVersion.
// This is designed to catch logic flaws within the operator that result in spurious updates to an underlying resource.
func WaitForStableResourceVersion(td TestData, o client.Object) {
	td.T.Helper()

	c := 0
	t := 2
	lrv := ""
	i := 0
	mi := 10

	RunOperatorUntil(td, func() error {
		err := td.Kube.Get(td.Ctx, client.ObjectKeyFromObject(o), o)
		td.Require.NoError(err, "fetch object while waiting for stable resource version")
		rv := o.GetResourceVersion()
		if lrv != rv {
			c += 1
			lrv = rv
		}
		i += 1
		if i < mi {
			return nil
		}
		return StopIteration{}
	})

	td.Require.True(c < t, "resource change threshold reached")
}

// Runs the operator for a fixed duration.  This is to ensure
// reconciliation happens for resources created by the operator that aren't tracked directly.
func WaitForDuration(td TestData, duration time.Duration) {
	now := time.Now()

	RunOperatorUntil(td, func() error {
		if time.Now().Sub(now) < duration {
			return nil
		}
		return StopIteration{}
	})
}

func TestAccessClaim(t *testing.T) {
	createAccessClaim := func(td TestData) *v1.AccessClaim {
		td.T.Helper()

		p := pod.DeepCopy()
		err := td.Kube.Create(td.Ctx, p)
		td.Require.NoError(err, "create pod")

		ac := serviceAccessClaim.DeepCopy()
		err = td.Kube.Create(td.Ctx, ac)
		td.Require.NoError(err, "create access claim")

		return ac
	}

	waitForReconcile := func(td TestData, ac *v1.AccessClaim) {
		RunOperatorUntil(td, func() error {
			err := td.Kube.Get(td.Ctx, client.ObjectKeyFromObject(ac), ac)
			td.Require.NoError(err, "wait for access claim reconcile")
			if ac.Status.CurrentSpec == nil {
				return nil
			}
			if !reflect.DeepEqual(*ac.Status.CurrentSpec, ac.Spec) {
				return nil
			}
			return StopIteration{}
		})
	}

	t.Run("adds finalizer", func(t *testing.T) {
		td := Setup(t)

		ac := createAccessClaim(td)
		waitForReconcile(td, ac)

		td.Require.True(controllerutil.ContainsFinalizer(ac, operator.Finalizer))
	})

	t.Run("creates a service access resource", func(t *testing.T) {
		td := Setup(t)

		ac := createAccessClaim(td)
		waitForReconcile(td, ac)

		a := &v1.Access{}
		err := td.Kube.Get(td.Ctx, client.ObjectKeyFromObject(ac), a)
		td.Require.NoError(err, "fetch created access resource")
		td.Require.Equal(ac.Spec.Dns, a.Spec.Dns)
		td.Require.Equal(ac.Spec.ServiceTemplates, a.Spec.ServiceTemplates)
	})

	t.Run("updates service access resource", func(t *testing.T) {
		td := Setup(t)

		ac := createAccessClaim(td)
		waitForReconcile(td, ac)

		ac.Spec.Dns = "other.example"
		ac.Spec.ServiceTemplates[0].Ports[0].Port = 9999
		err := td.Kube.Update(td.Ctx, ac)
		td.Require.NoError(err, "update access claim")
		waitForReconcile(td, ac)

		a := &v1.Access{}
		err = td.Kube.Get(td.Ctx, client.ObjectKeyFromObject(ac), a)
		td.Require.NoError(err, "fetch updated access resource")
		td.Require.Equal(ac.Spec.Dns, a.Spec.Dns)
		td.Require.Equal(ac.Spec.ServiceTemplates, a.Spec.ServiceTemplates)
	})

	t.Run("re-creates missing access resource", func(t *testing.T) {
		td := Setup(t)

		ac := createAccessClaim(td)
		waitForReconcile(td, ac)

		a := &v1.Access{}
		err := td.Kube.Get(td.Ctx, client.ObjectKeyFromObject(ac), a)
		td.Require.NoError(err, "fetch created access resource")

		err = td.Kube.Delete(td.Ctx, a)
		td.Require.NoError(err, "delete access resource")
		WaitForDelete(td, a)

		RunOperatorUntil(td, func() error {
			err := td.Kube.Get(td.Ctx, client.ObjectKeyFromObject(ac), a)
			if err != nil && apierrors.IsNotFound(err) {
				return nil
			}
			td.Require.NoError(err, "wait for access to reappear")
			return StopIteration{}
		})
	})

	t.Run("ensure resource version stabilizes", func(t *testing.T) {
		td := Setup(t)

		ac := createAccessClaim(td)
		waitForReconcile(td, ac)

		WaitForStableResourceVersion(td, ac)
	})

	t.Run("deletes access resource", func(t *testing.T) {
		td := Setup(t)

		ac := createAccessClaim(td)
		waitForReconcile(td, ac)

		err := td.Kube.Delete(td.Ctx, ac)
		td.Require.NoError(err, "delete access claim")
		WaitForDelete(td, ac)

		a := serviceAccess.DeepCopy()
		WaitForDelete(td, a)
	})
}

func TestAccess(t *testing.T) {
	createServiceAccess := func(td TestData) *v1.Access {
		td.T.Helper()

		p := pod.DeepCopy()
		err := td.Kube.Create(td.Ctx, p)
		td.Require.NoError(err, "create pod")

		a := serviceAccess.DeepCopy()
		err = td.Kube.Create(td.Ctx, a)
		td.Require.NoError(err, "create access")

		return a
	}

	createIngressAccess := func(td TestData) *v1.Access {
		td.T.Helper()

		p := pod.DeepCopy()
		err := td.Kube.Create(td.Ctx, p)
		td.Require.NoError(err, "create pod")

		s := service.DeepCopy()
		err = td.Kube.Create(td.Ctx, s)
		td.Require.NoError(err, "create service")

		a := ingressAccess.DeepCopy()
		err = td.Kube.Create(td.Ctx, a)
		td.Require.NoError(err, "create access")

		return a
	}

	waitForReconcile := func(td TestData, ac *v1.Access) {
		RunOperatorUntil(td, func() error {
			err := td.Kube.Get(td.Ctx, client.ObjectKeyFromObject(ac), ac)
			td.Require.NoError(err, "wait for access reconcile")
			if ac.Status.CurrentSpec == nil {
				return nil
			}
			if !reflect.DeepEqual(*ac.Status.CurrentSpec, ac.Spec) {
				return nil
			}
			return StopIteration{}
		})
	}

	getServices := func(td TestData, a *v1.Access) []*corev1.Service {
		sl := corev1.ServiceList{}
		err := td.Kube.List(td.Ctx, &sl, client.InNamespace(a.Namespace), client.MatchingLabels{operator.AccessLabel: a.Name})
		td.Require.NoError(err, "fetch access services")
		ss := []*corev1.Service{}
		for _, s := range sl.Items {
			ss = append(ss, &s)
		}
		return ss
	}

	getIngresses := func(td TestData, a *v1.Access) []*networkingv1.Ingress {
		il := networkingv1.IngressList{}
		err := td.Kube.List(td.Ctx, &il, client.InNamespace(a.Namespace), client.MatchingLabels{operator.AccessLabel: a.Name})
		td.Require.NoError(err, "fetch access ingresses")
		is := []*networkingv1.Ingress{}
		for _, i := range il.Items {
			is = append(is, &i)
		}
		return is
	}

	getCiliumNetworkPolicies := func(td TestData, a *v1.Access) []*ciliumv2.CiliumNetworkPolicy {
		cnpl := ciliumv2.CiliumNetworkPolicyList{}
		err := td.Kube.List(td.Ctx, &cnpl, client.InNamespace(a.Namespace), client.MatchingLabels{operator.AccessLabel: a.Name})
		td.Require.NoError(err, "fetch access cilium network policies")
		cnps := []*ciliumv2.CiliumNetworkPolicy{}
		for _, cnp := range cnpl.Items {
			cnps = append(cnps, &cnp)
		}
		return cnps
	}

	assignStaticIps := func(td TestData, a *v1.Access) {
		for _, i := range getIngresses(td, a) {
			if len(i.Status.LoadBalancer.Ingress) == 0 {
				i.Status.LoadBalancer.Ingress = append(i.Status.LoadBalancer.Ingress, networkingv1.IngressLoadBalancerIngress{})
			}
			i.Status.LoadBalancer.Ingress[0].IP = "255.255.255.255"
			err := td.Kube.Status().Update(td.Ctx, i)
			td.Require.NoError(err, "set ingress static ip")
		}

		for _, s := range getServices(td, a) {
			if len(s.Status.LoadBalancer.Ingress) == 0 {
				s.Status.LoadBalancer.Ingress = append(s.Status.LoadBalancer.Ingress, corev1.LoadBalancerIngress{})
			}
			s.Status.LoadBalancer.Ingress[0].IP = "255.255.255.255"
			err := td.Kube.Status().Update(td.Ctx, s)
			td.Require.NoError(err, "set service static ip")
		}
	}

	filterRouterosReply := func(a *v1.Access, r *routeros.Reply) []map[string]string {
		d := []map[string]string{}
		c := fmt.Sprintf("access-operator: %s/%s", a.Namespace, a.Name)
		for _, re := range r.Re {
			if re.Map["comment"] != c {
				continue
			}
			d = append(d, re.Map)
		}
		return d
	}

	deriveCiliumNetworkPolicySelector := func(s map[string]string) map[string]string {
		r := map[string]string{}
		for k, v := range s {
			r[fmt.Sprintf("any.%s", k)] = v
		}
		return r
	}

	t.Run("adds finalizer", func(t *testing.T) {
		td := Setup(t)

		a := createServiceAccess(td)
		waitForReconcile(td, a)

		td.Require.True(controllerutil.ContainsFinalizer(a, operator.Finalizer))
	})

	t.Run("creates service", func(t *testing.T) {
		td := Setup(t)

		a := createServiceAccess(td)
		waitForReconcile(td, a)

		ss := getServices(td, a)
		td.Require.Len(ss, 1)
		td.Require.Equal(a.Spec.ServiceTemplates[0].Ports[0].Port, ss[0].Spec.Ports[0].Port)
	})

	t.Run("creates ingress", func(t *testing.T) {
		td := Setup(t)

		a := createIngressAccess(td)
		waitForReconcile(td, a)

		is := getIngresses(td, a)
		td.Require.Len(is, 1)
		td.Require.Equal(a.Spec.IngressTemplate.DefaultBackend, is[0].Spec.DefaultBackend)
	})

	t.Run("patches service", func(t *testing.T) {
		td := Setup(t)

		a := createServiceAccess(td)
		waitForReconcile(td, a)

		a.Spec.ServiceTemplates[0].Ports[0].Port = 65432
		td.Kube.Update(td.Ctx, a)
		waitForReconcile(td, a)

		ss := getServices(td, a)
		td.Require.Len(ss, 1)
		td.Require.Equal(a.Spec.ServiceTemplates[0].Ports[0].Port, ss[0].Spec.Ports[0].Port)
	})

	t.Run("patches ingress", func(t *testing.T) {
		td := Setup(t)

		a := createIngressAccess(td)
		waitForReconcile(td, a)

		a.Spec.IngressTemplate.DefaultBackend.Service.Name = "other"
		a.Spec.IngressTemplate.DefaultBackend.Service.Port.Number = 1234
		td.Kube.Update(td.Ctx, a)
		waitForReconcile(td, a)

		is := getIngresses(td, a)
		td.Require.Len(is, 1)
		td.Require.Equal(a.Spec.IngressTemplate.DefaultBackend, is[0].Spec.DefaultBackend)
	})

	t.Run("adds additional service", func(t *testing.T) {
		td := Setup(t)

		a := createServiceAccess(td)
		waitForReconcile(td, a)

		ns := corev1.ServiceSpec{
			Ports:    []corev1.ServicePort{{Port: 1234, Protocol: "UDP"}},
			Selector: a.Spec.ServiceTemplates[0].Selector,
		}
		a.Spec.ServiceTemplates = append(a.Spec.ServiceTemplates, ns)
		td.Kube.Update(td.Ctx, a)
		waitForReconcile(td, a)

		ss := getServices(td, a)
		td.Require.Len(ss, 2)
	})

	t.Run("removes service", func(t *testing.T) {
		td := Setup(t)

		a := createServiceAccess(td)
		waitForReconcile(td, a)

		a.Spec.ServiceTemplates = nil
		td.Kube.Update(td.Ctx, a)
		waitForReconcile(td, a)

		ss := getServices(td, a)
		td.Require.Len(ss, 0)
	})

	t.Run("removes ingress", func(t *testing.T) {
		td := Setup(t)

		a := createServiceAccess(td)
		waitForReconcile(td, a)

		a.Spec.IngressTemplate = nil
		td.Kube.Update(td.Ctx, a)
		waitForReconcile(td, a)

		is := getIngresses(td, a)
		td.Require.Len(is, 0)
	})

	t.Run("creates service cilium network policy", func(t *testing.T) {
		td := Setup(t)

		a := createServiceAccess(td)
		waitForReconcile(td, a)
		assignStaticIps(td, a)
		WaitForDuration(td, 1*time.Second)

		cnps := getCiliumNetworkPolicies(td, a)
		td.Require.Len(cnps, 1)

		p := pod.DeepCopy()
		err := td.Kube.Get(td.Ctx, client.ObjectKeyFromObject(p), p)
		td.Require.NoError(err, "fetch pod")

		td.Require.Equal(cnps[0].Specs[0].EndpointSelector.MatchLabels, deriveCiliumNetworkPolicySelector(a.Spec.ServiceTemplates[0].Selector))
		td.Require.Len(cnps[0].Specs[0].Ingress, 1)
		td.Require.Equal(cnps[0].Specs[0].Ingress[0].ToPorts[0].Ports[0].Port, strconv.Itoa(int(p.Spec.Containers[0].Ports[0].ContainerPort)))
		td.Require.Equal(string(cnps[0].Specs[0].Ingress[0].ToPorts[0].Ports[0].Protocol), string(p.Spec.Containers[0].Ports[0].Protocol))
		td.Require.Len(cnps[0].Specs[0].Egress, 0)
	})

	t.Run("creates ingress cilium network policy", func(t *testing.T) {
		td := Setup(t)

		a := createIngressAccess(td)
		waitForReconcile(td, a)
		assignStaticIps(td, a)
		WaitForDuration(td, 1*time.Second)

		cnps := getCiliumNetworkPolicies(td, a)
		td.Require.Len(cnps, 1)

		s := service.DeepCopy()
		err := td.Kube.Get(td.Ctx, client.ObjectKeyFromObject(s), s)
		td.Require.NoError(err, "fetch service")

		p := pod.DeepCopy()
		err = td.Kube.Get(td.Ctx, client.ObjectKeyFromObject(p), p)
		td.Require.NoError(err, "fetch pod")

		td.Require.Len(cnps[0].Specs[0].EndpointSelector.MatchExpressions, 1)
		td.Require.Equal(cnps[0].Specs[0].EndpointSelector.MatchExpressions[0].Key, "reserved.ingress")
		td.Require.Equal(string(cnps[0].Specs[0].EndpointSelector.MatchExpressions[0].Operator), "Exists")
		td.Require.Len(cnps[0].Specs[0].Ingress, 1)
		td.Require.Equal(cnps[0].Specs[0].Ingress[0].ToPorts[0].Ports[0].Port, strconv.Itoa(int(p.Spec.Containers[0].Ports[0].ContainerPort)))
		td.Require.Equal(string(cnps[0].Specs[0].Ingress[0].ToPorts[0].Ports[0].Protocol), string(p.Spec.Containers[0].Ports[0].Protocol))
		td.Require.Len(cnps[0].Specs[0].Egress, 1)
		td.Require.Equal(cnps[0].Specs[0].Egress[0].ToEndpoints[0].MatchLabels, deriveCiliumNetworkPolicySelector(s.Spec.Selector))
		td.Require.Equal(cnps[0].Specs[0].Egress[0].ToPorts[0].Ports[0].Port, strconv.Itoa(int(p.Spec.Containers[0].Ports[0].ContainerPort)))
		td.Require.Equal(string(cnps[0].Specs[0].Egress[0].ToPorts[0].Ports[0].Protocol), string(p.Spec.Containers[0].Ports[0].Protocol))
	})

	t.Run("creates routeros filter rule", func(t *testing.T) {
		td := Setup(t)

		a := createServiceAccess(td)
		waitForReconcile(td, a)

		re, err := td.Routeros.Run("/ip/firewall/filter/print", "detail")
		td.Require.NoError(err, "list firewall filters")
		fs := filterRouterosReply(a, re)
		td.Require.Len(fs, 1)

		f := fs[0]
		td.Require.Equal(f["action"], "accept")
		td.Require.Equal(f["chain"], "input")
		td.Require.Equal(f["src-address-list"], fmt.Sprintf("access-operator/%s/%s", a.Namespace, a.Name))
		td.Require.Equal(f["disabled"], "true")
	})

	t.Run("creates service routeros nat rule", func(t *testing.T) {
		td := Setup(t)

		a := createServiceAccess(td)
		waitForReconcile(td, a)
		assignStaticIps(td, a)
		WaitForDuration(td, 1*time.Second)

		ss := getServices(td, a)
		td.Require.Len(ss, 1)
		td.Require.Len(ss[0].Status.LoadBalancer.Ingress, 1)
		ip := ss[0].Status.LoadBalancer.Ingress[0].IP
		p := ss[0].Spec.Ports[0].Port
		pr := ss[0].Spec.Ports[0].Protocol
		if pr == "" {
			pr = "TCP"
		}

		re, err := td.Routeros.Run("/ip/firewall/nat/print", "detail")
		td.Require.NoError(err, "list firewall nats")
		ns := filterRouterosReply(a, re)
		td.Require.Len(ns, 1)

		n := ns[0]
		td.Require.Equal(n["action"], "dst-nat")
		td.Require.Equal(n["chain"], "dstnat")
		td.Require.Equal(n["src-address-list"], fmt.Sprintf("access-operator/%s/%s", a.Namespace, a.Name))
		td.Require.Equal(n["protocol"], strings.ToLower(string(pr)))
		td.Require.Equal(n["dst-port"], strconv.Itoa(int(p)))
		td.Require.Equal(n["to-addresses"], ip)
		td.Require.Equal(n["to-ports"], strconv.Itoa(int(p)))
		td.Require.Equal(n["disabled"], "true")
	})

	t.Run("creates ingress routeros nat rule", func(t *testing.T) {
		td := Setup(t)

		a := createIngressAccess(td)
		waitForReconcile(td, a)
		assignStaticIps(td, a)
		WaitForDuration(td, 1*time.Second)

		is := getIngresses(td, a)
		td.Require.Len(is, 1)
		ip := is[0].Status.LoadBalancer.Ingress[0].IP

		s := service.DeepCopy()
		err := td.Kube.Get(td.Ctx, client.ObjectKeyFromObject(s), s)
		td.Require.NoError(err, "fetch service")
		p := s.Spec.Ports[0].Port
		pr := s.Spec.Ports[0].Protocol
		if pr == "" {
			pr = "TCP"
		}

		re, err := td.Routeros.Run("/ip/firewall/nat/print", "detail")
		td.Require.NoError(err, "list firewall nats")
		ns := filterRouterosReply(a, re)
		td.Require.Len(ns, 1)

		n := ns[0]
		td.Require.Equal(n["action"], "dst-nat")
		td.Require.Equal(n["chain"], "dstnat")
		td.Require.Equal(n["src-address-list"], fmt.Sprintf("access-operator/%s/%s", a.Namespace, a.Name))
		td.Require.Equal(n["protocol"], strings.ToLower(string(pr)))
		td.Require.Equal(n["dst-port"], strconv.Itoa(int(p)))
		td.Require.Equal(n["to-addresses"], ip)
		td.Require.Equal(n["to-ports"], strconv.Itoa(int(p)))
		td.Require.Equal(n["disabled"], "true")
	})

	t.Run("creates routeros address list", func(t *testing.T) {
		td := Setup(t)

		a := createServiceAccess(td)
		a.Spec.Members = append(a.Spec.Members, v1.Member{Cidr: "252.252.252.252/32", Expires: time.Now().Add(1 * time.Hour).Format(time.RFC3339)})
		err := td.Kube.Update(td.Ctx, a)
		td.Require.NoError(err, "update access")
		waitForReconcile(td, a)

		re, err := td.Routeros.Run("/ip/firewall/address-list/print", "detail")
		td.Require.NoError(err, "list firewall address lists")
		als := filterRouterosReply(a, re)
		td.Require.Len(als, 1)

		al := als[0]
		td.Require.Equal(al["address"], strings.Replace(a.Spec.Members[0].Cidr, "/32", "", -1))
		td.Require.Equal(al["comment"], fmt.Sprintf("access-operator: %s/%s", a.Namespace, a.Name))
		td.Require.Equal(al["list"], fmt.Sprintf("access-operator/%s/%s", a.Namespace, a.Name))
	})

	t.Run("updates routeros address list", func(t *testing.T) {
		td := Setup(t)

		a := createServiceAccess(td)
		a.Spec.Members = append(a.Spec.Members, v1.Member{Cidr: "252.252.252.252/32", Expires: time.Now().Add(1 * time.Hour).Format(time.RFC3339)})
		err := td.Kube.Update(td.Ctx, a)
		td.Require.NoError(err, "update access")
		waitForReconcile(td, a)

		a.Spec.Members = append(a.Spec.Members, v1.Member{Cidr: "251.251.251.251/32", Expires: time.Now().Add(1 * time.Hour).Format(time.RFC3339)})
		err = td.Kube.Update(td.Ctx, a)
		td.Require.NoError(err, "update access")
		waitForReconcile(td, a)

		re, err := td.Routeros.Run("/ip/firewall/address-list/print", "detail")
		td.Require.NoError(err, "list firewall address lists")
		als := filterRouterosReply(a, re)
		td.Require.Len(als, 2)

		al := als[1]
		td.Require.Equal(al["address"], strings.Replace(a.Spec.Members[1].Cidr, "/32", "", -1))
		td.Require.Equal(al["list"], fmt.Sprintf("access-operator/%s/%s", a.Namespace, a.Name))
	})

	t.Run("deletes routeros filter rule", func(t *testing.T) {
		td := Setup(t)

		a := createServiceAccess(td)
		waitForReconcile(td, a)

		err := td.Kube.Delete(td.Ctx, a)
		td.Require.NoError(err, "delete access")
		waitForReconcile(td, a)

		re, err := td.Routeros.Run("/ip/firewall/filter/print", "detail")
		td.Require.NoError(err, "list filters")
		fs := filterRouterosReply(a, re)
		td.Require.Len(fs, 0)
	})

	t.Run("deletes routeros nat rule", func(t *testing.T) {
		td := Setup(t)

		a := createServiceAccess(td)
		waitForReconcile(td, a)

		err := td.Kube.Delete(td.Ctx, a)
		td.Require.NoError(err, "delete access")
		waitForReconcile(td, a)

		re, err := td.Routeros.Run("/ip/firewall/nat/print", "detail")
		td.Require.NoError(err, "list nats")
		ns := filterRouterosReply(a, re)
		td.Require.Len(ns, 0)
	})

	t.Run("deletes routeros address list", func(t *testing.T) {
		td := Setup(t)

		a := createServiceAccess(td)
		waitForReconcile(td, a)

		err := td.Kube.Delete(td.Ctx, a)
		td.Require.NoError(err, "delete access")
		waitForReconcile(td, a)

		re, err := td.Routeros.Run("/ip/firewall/address-list/print", "detail")
		td.Require.NoError(err, "list address lists")
		als := filterRouterosReply(a, re)
		td.Require.Len(als, 0)
	})

	t.Run("ensure service resource version stabilizes", func(t *testing.T) {
		td := Setup(t)

		a := createServiceAccess(td)
		waitForReconcile(td, a)

		WaitForStableResourceVersion(td, a)
	})

	t.Run("ensure ingress resource version stabilizes", func(t *testing.T) {
		td := Setup(t)

		a := createIngressAccess(td)
		waitForReconcile(td, a)

		WaitForStableResourceVersion(td, a)
	})
}
