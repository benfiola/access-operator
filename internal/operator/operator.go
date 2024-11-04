package operator

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	v1 "github.com/benfiola/access-operator/pkg/api/bfiola.dev/v1"
	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	ciliummetav1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/apis/meta/v1"
	"github.com/cilium/cilium/pkg/policy/api"
	corev1 "k8s.io/api/core/v1"

	"github.com/go-logr/logr"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	Finalizer        = "bfiola.dev/access-operator"
	IndexLabel       = "bfiola.dev/service-index"
	AccessLabel      = "bfiola.dev/access"
	AccessClaimLabel = "bfiola.dev/access-claim"
)

// Operator is the public interface for the operator implementation
type Operator interface {
	Health() error
	Run(ctx context.Context) error
}

// operator manages all of the crd controllers
type operator struct {
	manager manager.Manager
	logger  *slog.Logger
}

// OperatorOpts defines the options used to construct a new [operator]
type OperatorOpts struct {
	HealthBindAddress string
	Kubeconfig        string
	Logger            *slog.Logger
	RouterOsAddress   string
	RouterOsPassword  string
	RouterOsUsername  string
	SyncInterval      time.Duration
}

// Creates a new operator with the provided [OperatorOpts].
func NewOperator(o *OperatorOpts) (*operator, error) {
	l := o.Logger
	if l == nil {
		l = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	c, err := clientcmd.BuildConfigFromFlags("", o.Kubeconfig)
	if err != nil {
		return nil, err
	}

	si := o.SyncInterval
	if si == 0 {
		si = 60 * time.Second
	}

	s := runtime.NewScheme()
	err = v1.AddToScheme(s)
	if err != nil {
		return nil, err
	}
	err = corev1.AddToScheme(s)
	if err != nil {
		return nil, err
	}
	err = networkingv1.AddToScheme(s)
	if err != nil {
		return nil, err
	}
	err = ciliumv2.AddToScheme(s)
	if err != nil {
		return nil, err
	}

	hba := o.HealthBindAddress
	if hba == "" {
		hba = ":8888"
	}

	m, err := manager.New(c, manager.Options{
		Client:                 client.Options{Cache: &client.CacheOptions{DisableFor: []client.Object{&corev1.Pod{}, &corev1.Service{}, &networkingv1.Ingress{}}}},
		Controller:             config.Controller{SkipNameValidation: ptr(true)},
		HealthProbeBindAddress: hba,
		Logger:                 logr.FromSlogHandler(l.Handler()),
		Metrics:                server.Options{BindAddress: "0"},
		Scheme:                 s,
	})
	if err != nil {
		return nil, err
	}
	err = m.AddHealthzCheck("healthz", healthz.Ping)
	if err != nil {
		return nil, err
	}
	err = m.AddReadyzCheck("readyz", healthz.Ping)
	if err != nil {
		return nil, err
	}

	roc, err := NewRouterOsClient(&RouterOsClientOpts{
		Address:  o.RouterOsAddress,
		Password: o.RouterOsPassword,
		Logger:   l.With("name", "routeros"),
		Username: o.RouterOsUsername,
	})

	rs := []reconciler{
		&accessClaimReconciler{},
		&accessReconciler{
			routerOs: roc,
		},
	}
	for _, r := range rs {
		err = r.register(m)
		if err != nil {
			return nil, err
		}
	}

	return &operator{
		logger:  l,
		manager: m,
	}, err
}

// Performs a health check for the given [operator],
// Returns an error if the [operator] is unhealthy.
func (o *operator) Health() error {
	return nil
}

// Starts the [operator].
// Runs until terminated or if an error is thrown.
func (o *operator) Run(ctx context.Context) error {
	o.logger.Info("starting operator")
	err := o.manager.Start(ctx)
	if err != nil {
		return err
	}
	return nil
}

// reconciler is the common interface implemented by all CRD reconcilers in this package
type reconciler interface {
	reconcile.Reconciler
	register(m manager.Manager) error
}

// accessClaimReconciler reconciles [v1.AccessClaim] resources
type accessClaimReconciler struct {
	client.Client
	logger       logr.Logger
	syncInterval time.Duration
}

// +kubebuilder:rbac:groups=bfiola.dev,resources=accessclaims,verbs=get;list;update;watch
// +kubebuilder:rbac:groups=bfiola.dev,resources=accesses,verbs=create;list;update;watch

// Builds a controller with a [accessClaimReconciler].
// Registers this controller with a [manager.Manager] instance.
// Returns an error if a controller cannot be built.
func (r *accessClaimReconciler) register(m manager.Manager) error {
	r.Client = m.GetClient()
	ctrl, err := builder.ControllerManagedBy(m).For(&v1.AccessClaim{}).Owns(&v1.Access{}).Build(r)
	if err != nil {
		return err
	}
	r.logger = ctrl.GetLogger()
	return nil
}

// Reconciles a [reconcile.Request] associated with a [v1.AccessClaim].
// Returns a error if reconciliation fails.
func (r *accessClaimReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	l := r.logger.WithValues("resource", req.NamespacedName.String())

	ac := &v1.AccessClaim{}
	err := r.Get(ctx, req.NamespacedName, ac)
	if err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	success := func() (reconcile.Result, error) { return reconcile.Result{RequeueAfter: r.syncInterval}, nil }
	failure := func(err error) (reconcile.Result, error) { return reconcile.Result{}, err }

	if !ac.ObjectMeta.DeletionTimestamp.IsZero() {
		l.Info("clear status and finalizers")
		ac.Status.CurrentSpec = nil
		controllerutil.RemoveFinalizer(ac, Finalizer)
		err = r.Update(ctx, ac)
		if err != nil {
			return failure(err)
		}

		return success()
	}

	if !controllerutil.ContainsFinalizer(ac, Finalizer) {
		l.Info("add finalizer")
		controllerutil.AddFinalizer(ac, Finalizer)
		err = r.Update(ctx, ac)
		if err != nil {
			return failure(err)
		}

		return success()
	}

	l.Info("get existing accesses")
	al := &v1.AccessList{}
	err = r.Client.List(ctx, al, client.InNamespace(ac.Namespace), client.MatchingLabels{AccessClaimLabel: ac.Name})
	if err != nil {
		return failure(err)
	}
	var a *v1.Access
	if len(al.Items) > 1 {
		for _, a := range al.Items[1:] {
			l.Info(fmt.Sprintf("delete access %s/%s", a.Namespace, a.Name))
			err := r.Delete(ctx, &a)
			if err != nil {
				return failure(err)
			}
		}
	}
	if len(al.Items) >= 1 {
		a = &al.Items[0]
	}
	if a == nil {
		om := metav1.ObjectMeta{Namespace: ac.Namespace, Name: ac.Name, Labels: map[string]string{
			AccessClaimLabel: ac.Name,
		}}
		l.Info("create access %s/%s", om.Namespace, om.Name)
		a = &v1.Access{ObjectMeta: om, Spec: v1.AccessSpec{
			Dns:              ac.Spec.Dns,
			IngressTemplate:  ac.Spec.IngressTemplate,
			Members:          []v1.Member{},
			PasswordRef:      ac.Spec.PasswordRef,
			ServiceTemplates: ac.Spec.ServiceTemplates,
			Ttl:              ac.Spec.Ttl,
		}}
		controllerutil.SetOwnerReference(ac, a, r.Scheme())
		err := r.Client.Create(ctx, a)
		if err != nil {
			return failure(err)
		}
	} else if a.Spec.Dns != ac.Spec.Dns || !reflect.DeepEqual(a.Spec.IngressTemplate, ac.Spec.IngressTemplate) || !reflect.DeepEqual(a.Spec.PasswordRef, ac.Spec.PasswordRef) || !reflect.DeepEqual(a.Spec.ServiceTemplates, ac.Spec.ServiceTemplates) || a.Spec.Ttl != ac.Spec.Ttl {
		l.Info("update access %s/%s", a.Namespace, a.Name)
		a.Spec.Dns = ac.Spec.Dns
		a.Spec.IngressTemplate = ac.Spec.IngressTemplate
		a.Spec.PasswordRef = ac.Spec.PasswordRef
		a.Spec.ServiceTemplates = ac.Spec.ServiceTemplates
		a.Spec.Ttl = ac.Spec.Ttl
		err = r.Update(ctx, a)
	}
	if err != nil {
		return failure(err)
	}

	l.Info("set status")
	ac.Status.CurrentSpec = &ac.Spec
	err = r.Update(ctx, ac)
	if err != nil {
		return failure(err)
	}

	return success()
}

// accessReconciler reconciles [v1.Access] resources
type accessReconciler struct {
	client.Client
	logger       logr.Logger
	routerOs     RouterOsClient
	syncInterval time.Duration
}

// +kubebuilder:rbac:groups=bfiola.dev,resources=accesses,verbs=get;list;update;watch
// +kubebuilder:rbac:groups=core,resources=services,verbs=create;list;patch;watch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=create;list;update;watch
// +kubebuilder:rbac:groups=cilium.io,resources=ciliumnetworkpolicies,verbs=create;list;update;watch

// Builds a controller with a [accessReconciler].
// Registers this controller with a [manager.Manager] instance.
// Returns an error if a controller cannot be built.
func (r *accessReconciler) register(m manager.Manager) error {
	r.Client = m.GetClient()
	ctrl, err := builder.ControllerManagedBy(m).For(&v1.Access{}).Owns(&corev1.Service{}).Owns(&networkingv1.Ingress{}).Owns(&ciliumv2.CiliumNetworkPolicy{}).Build(r)
	if err != nil {
		return err
	}
	r.logger = ctrl.GetLogger()
	return nil
}

// accessRule defines connectivity to a specific service.
// This information is used to construct firewall rules - as requests made through the firewall are dstnat'ed to the type 'LoadBalancer' [corev1.Service].
// See: [AccessInfo]
type accessRule struct {
	FromPort int
	Ingress  bool
	Protocol string
	ToIp     string
	ToPort   string
	Selector map[string]string
}

// AccessInfo defines connectivity information for a [v1.Access] resource
type AccessInfo struct {
	FromCidrs []string
	Rules     []accessRule
}

// Helper method that returns a list of pods matching a [corev1.Service]'s pod selector
func (ai *AccessInfo) getPods(c client.Client, s *corev1.Service) ([]*corev1.Pod, error) {
	ctx := context.Background()
	pl := &corev1.PodList{}
	err := c.List(ctx, pl, client.MatchingLabels(s.Spec.Selector))
	ps := []*corev1.Pod{}
	for _, p := range pl.Items {
		ps = append(ps, &p)
	}
	return ps, err
}

// Collects information about a provided [corev1.Service] and adds this information to the [AccessInfo]
// Returns an error if attempts to collect information fail
func (ai *AccessInfo) AddService(c client.Client, s *corev1.Service) error {
	if s.Spec.Type != "LoadBalancer" {
		// do nothing if service not of type 'LoadBalancer'
		return nil
	}
	if len(s.Status.LoadBalancer.Ingress) != 1 {
		// do nothing if service does not have assigned ip address
		return nil
	}

	// determine container ports
	for _, sp := range s.Spec.Ports {
		if sp.Port == 80 || sp.Port == 443 {
			// ignore http/s ports
			continue
		}
		pr := string(sp.Protocol)
		if pr == "" {
			pr = "TCP"
		}
		p := int(sp.Port)
		tp := strconv.Itoa(p)
		if sp.TargetPort.Type == intstr.Int {
			tp = strconv.Itoa(int(sp.TargetPort.IntVal))
		} else if sp.TargetPort.Type == intstr.String {
			tp = sp.TargetPort.StrVal
		}
		r := accessRule{
			FromPort: p,
			Protocol: pr,
			Selector: s.Spec.Selector,
			ToIp:     s.Status.LoadBalancer.Ingress[0].IP,
			ToPort:   tp,
		}
		ai.Rules = append(ai.Rules, r)
	}

	return nil
}

// Collects information about a provided [networkingv1.Ingress] and adds this information to the [AccessInfo]
// Returns an error if attempts to collect information fail
func (ai *AccessInfo) AddIngress(c client.Client, i *networkingv1.Ingress) error {
	ctx := context.Background()

	// handle ingress resource
	if len(i.Status.LoadBalancer.Ingress) != 1 {
		// do nothing when ingress does not have assigned ip address
		return nil
	}

	// collect all backends for the ingress
	bes := []networkingv1.IngressServiceBackend{}
	if i.Spec.DefaultBackend != nil && i.Spec.DefaultBackend.Service != nil {
		// the default backend is the fallback and will always be used
		bes = append(bes, *i.Spec.DefaultBackend.Service)
	}
	for _, r := range i.Spec.Rules {
		if r.HTTP != nil {
			for _, p := range r.HTTP.Paths {
				if p.Backend.Service != nil {
					bes = append(bes, *p.Backend.Service)
				}
			}
		}
	}

	// assemble data from collected backends
	for _, be := range bes {
		s := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: i.Namespace, Name: be.Name}}
		err := c.Get(ctx, client.ObjectKeyFromObject(s), s)
		if err != nil {
			// fail if ingress backend not fetchable
			return err
		}

		// find service port mapped to ingress
		sp := corev1.ServicePort{}
		for _, p := range s.Spec.Ports {
			if be.Port.Name != "" && be.Port.Name == p.Name {
				sp = p
				break
			}
			if be.Port.Number != 0 && be.Port.Number == p.Port {
				sp = p
				break
			}
		}
		if (sp == corev1.ServicePort{}) {
			continue
		}

		// determine container ports
		if sp.Port != 80 && sp.Port != 443 {
			continue
		}
		pr := string(sp.Protocol)
		if pr == "" {
			pr = "TCP"
		}
		if pr != "TCP" {
			continue
		}
		p := int(sp.Port)
		tp := strconv.Itoa(p)
		if sp.TargetPort.Type == intstr.Int {
			tp = strconv.Itoa(int(sp.TargetPort.IntVal))
		} else if sp.TargetPort.Type == intstr.String {
			tp = sp.TargetPort.StrVal
		}
		r := accessRule{
			FromPort: p,
			Ingress:  true,
			Protocol: pr,
			Selector: s.Spec.Selector,
			ToIp:     i.Status.LoadBalancer.Ingress[0].IP,
			ToPort:   tp,
		}
		ai.Rules = append(ai.Rules, r)
	}

	return nil
}

// Reconciles a [reconcile.Request] associated with a [v1.Access].
// Returns a error if reconciliation fails.
func (r *accessReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	l := r.logger.WithValues("resource", req.NamespacedName.String())

	a := &v1.Access{}
	err := r.Get(ctx, req.NamespacedName, a)
	if err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	success := func() (reconcile.Result, error) { return reconcile.Result{RequeueAfter: r.syncInterval}, nil }
	failure := func(err error) (reconcile.Result, error) { return reconcile.Result{}, err }

	if !a.ObjectMeta.DeletionTimestamp.IsZero() {
		l.Info("deleting routeros rules")
		err := r.routerOs.Delete(a.Namespace, a.Name)
		if err != nil {
			return failure(err)
		}

		l.Info("clear status and finalizers")
		a.Status.CurrentSpec = nil
		controllerutil.RemoveFinalizer(a, Finalizer)
		err = r.Update(ctx, a)
		if err != nil {
			return failure(err)
		}

		return success()
	}

	if !controllerutil.ContainsFinalizer(a, Finalizer) {
		l.Info("add finalizer")
		controllerutil.AddFinalizer(a, Finalizer)
		err = r.Update(ctx, a)
		if err != nil {
			return failure(err)
		}

		return success()
	}

	n := time.Now()
	ms := []v1.Member{}
	ims := []v1.Member{}
	for _, m := range a.Spec.Members {
		if strings.HasPrefix(m.Cidr, "192.168.") {
			ims = append(ims, m)
			continue
		}
		e, err := time.Parse(time.RFC3339, m.Expires)
		if err != nil {
			return failure(err)
		}
		if e.Compare(n) < 0 {
			ims = append(ims, m)
		} else {
			ms = append(ms, m)
		}
	}
	if len(ims) != 0 {
		for _, m := range ims {
			l.Info("remove invalid member: %s", m.Cidr)
		}
		a.Spec.Members = ms
		err := r.Update(ctx, a)
		if err != nil {
			return failure(err)
		}
	}

	cs := []string{}
	for _, m := range a.Spec.Members {
		cs = append(cs, m.Cidr)
	}
	ai := AccessInfo{FromCidrs: cs}

	l.Info("get existing ingresses")
	il := &networkingv1.IngressList{}
	err = r.Client.List(ctx, il, client.InNamespace(a.Namespace), client.MatchingLabels{AccessLabel: a.Name})
	if err != nil {
		return failure(err)
	}
	var i *networkingv1.Ingress
	if len(il.Items) > 1 {
		for _, i := range il.Items[1:] {
			l.Info(fmt.Sprintf("delete ingress %s/%s", i.Namespace, i.Name))
			err := r.Delete(ctx, &i)
			if err != nil {
				return failure(err)
			}
		}
	}
	if len(il.Items) >= 1 {
		i = &il.Items[0]
	}
	if a.Spec.IngressTemplate != nil {
		ch := false
		for _, r := range a.Spec.IngressTemplate.Rules {
			if r.Host == a.Spec.Dns {
				continue
			}
			r.Host = a.Spec.Dns
			ch = true
		}
		if ch {
			l.Info("set ingress template defaults")
			err := r.Update(ctx, a)
			if err != nil {
				return failure(err)
			}
		}

		if i == nil {
			om := metav1.ObjectMeta{Namespace: a.Namespace, Name: fmt.Sprintf("access-%s", a.Name), Labels: map[string]string{
				AccessLabel: a.Name,
			}}
			l.Info(fmt.Sprintf("create ingress %s/%s", om.Namespace, om.Name))
			i = &networkingv1.Ingress{ObjectMeta: om, Spec: *a.Spec.IngressTemplate}
			controllerutil.SetOwnerReference(a, i, r.Scheme())
			err := r.Client.Create(ctx, i)
			if err != nil {
				return failure(err)
			}
		} else {
			l.Info(fmt.Sprintf("update ingress %s/%s", i.Namespace, i.Name))
			i.Spec = *a.Spec.IngressTemplate
			err := r.Update(ctx, i)
			if err != nil {
				return failure(err)
			}
		}
		l.Info(fmt.Sprintf("assemble rules from ingress %s/%s", i.Namespace, i.Name))
		err = ai.AddIngress(r, i)
		if err != nil {
			return failure(err)
		}
	} else if i != nil {
		l.Info(fmt.Sprintf("delete ingress %s/%s", i.Namespace, i.Name))
		err := r.Delete(ctx, i)
		if err != nil {
			return failure(err)
		}
	}

	sis := []int{}
	for i, st := range a.Spec.ServiceTemplates {
		if st.Type != "LoadBalancer" {
			sis = append(sis, i)
			st.Type = "LoadBalancer"
		}
	}
	if len(sis) > 0 {
		for _, i := range sis {
			l.Info("set service template %d defaults", i)
		}
		err := r.Update(ctx, a)
		if err != nil {
			return failure(err)
		}
	}

	l.Info("get existing services")
	sl := &corev1.ServiceList{}
	err = r.List(ctx, sl, client.InNamespace(a.Namespace), client.MatchingLabels{AccessLabel: a.Name})
	if err != nil {
		return failure(err)
	}
	slices.SortFunc(sl.Items, func(a corev1.Service, b corev1.Service) int {
		ai, err := strconv.Atoi(a.ObjectMeta.Labels[IndexLabel])
		if err != nil {
			ai = len(sl.Items)
		}
		bi, err := strconv.Atoi(b.ObjectMeta.Labels[IndexLabel])
		if err != nil {
			bi = len(sl.Items)
		}
		return ai - bi
	})
	ix := 0
	for {
		var st corev1.ServiceSpec
		var s corev1.Service
		if ix < len(a.Spec.ServiceTemplates) {
			st = a.Spec.ServiceTemplates[ix]
		}
		if ix < len(sl.Items) {
			s = sl.Items[ix]
		}

		if reflect.DeepEqual(s, corev1.Service{}) && reflect.DeepEqual(st, corev1.ServiceSpec{}) {
			break
		} else if !reflect.DeepEqual(s, corev1.Service{}) && reflect.DeepEqual(st, corev1.ServiceSpec{}) {
			l.Info(fmt.Sprintf("delete service %s/%s", s.Namespace, s.Name))
			err := r.Delete(ctx, &s)
			if err != nil {
				return failure(err)
			}
		} else if reflect.DeepEqual(s, corev1.Service{}) && !reflect.DeepEqual(st, corev1.ServiceSpec{}) {
			om := metav1.ObjectMeta{Namespace: a.Namespace, Name: fmt.Sprintf("access-%s-%d", a.Name, ix), Labels: map[string]string{
				AccessLabel: a.Name,
				IndexLabel:  strconv.Itoa(ix),
			}}
			l.Info(fmt.Sprintf("create service %s/%s", om.Namespace, om.Name))
			s = corev1.Service{ObjectMeta: om, Spec: st}
			controllerutil.SetOwnerReference(a, &s, r.Scheme())
			err := r.Client.Create(ctx, &s)
			if err != nil {
				return failure(err)
			}
		} else {
			l.Info(fmt.Sprintf("patch service %s/%s", s.Namespace, s.Name))
			os := s.DeepCopy()
			s.Spec = st
			err := r.Client.Patch(ctx, &s, client.StrategicMergeFrom(os))
			if err != nil {
				return failure(err)
			}
		}
		l.Info(fmt.Sprintf("assemble rules from service %s/%s", s.Namespace, s.Name))
		err := ai.AddService(r, &s)
		if err != nil {
			return failure(err)
		}
		ix = ix + 1
	}

	l.Info("sync routeros")
	err = r.routerOs.Sync(a.Namespace, a.Name, ai)
	if err != nil {
		return failure(err)
	}

	// create access cilium network policy rules
	cnprs := api.Rules{}
	cnpcs := []api.CIDR{}
	for _, c := range ai.FromCidrs {
		cnpcs = append(cnpcs, api.CIDR(c))
	}
	for _, r := range ai.Rules {
		var cnpr api.Rule
		if r.Ingress {
			cnpr = api.Rule{
				EndpointSelector: api.NewESFromMatchRequirements(nil, []ciliummetav1.LabelSelectorRequirement{
					{Key: "reserved.ingress", Operator: "Exists"},
				}),
				Ingress: []api.IngressRule{{
					IngressCommonRule: api.IngressCommonRule{
						FromCIDR: cnpcs,
					},
					ToPorts: api.PortRules{{Ports: []api.PortProtocol{{
						Port:     strconv.Itoa(r.FromPort),
						Protocol: api.L4Proto(r.Protocol),
					}}}},
				}},
				Egress: []api.EgressRule{{
					EgressCommonRule: api.EgressCommonRule{ToEndpoints: []api.EndpointSelector{
						api.NewESFromMatchRequirements(r.Selector, nil),
					}},
					ToPorts: api.PortRules{{Ports: []api.PortProtocol{{
						Port:     r.ToPort,
						Protocol: api.L4Proto(r.Protocol),
					}}}},
				}},
			}
		} else {
			cnpr = api.Rule{
				EndpointSelector: api.NewESFromMatchRequirements(r.Selector, nil),
				Ingress: []api.IngressRule{{
					IngressCommonRule: api.IngressCommonRule{
						FromCIDR: cnpcs,
					},
					ToPorts: []api.PortRule{{Ports: []api.PortProtocol{{
						Port:     r.ToPort,
						Protocol: api.L4Proto(r.Protocol),
					}}}},
				}},
			}
		}
		cnprs = append(cnprs, &cnpr)
	}

	l.Info("get existing cilium network policies")
	cnpl := &ciliumv2.CiliumNetworkPolicyList{}
	err = r.Client.List(ctx, cnpl, client.InNamespace(a.Namespace), client.MatchingLabels{AccessLabel: a.Name})
	if err != nil {
		return failure(err)
	}
	var cnp *ciliumv2.CiliumNetworkPolicy
	if len(cnpl.Items) > 1 {
		for _, cnp := range cnpl.Items[1:] {
			l.Info(fmt.Sprintf("delete cilium network policy %s/%s", cnp.Namespace, cnp.Name))
			err := r.Delete(ctx, &cnp)
			if err != nil {
				return failure(err)
			}
		}
	}
	if len(cnpl.Items) >= 1 {
		cnp = &cnpl.Items[0]
	}
	if cnp == nil {
		om := metav1.ObjectMeta{Namespace: a.Namespace, Name: fmt.Sprintf("access-%s", a.Name), Labels: map[string]string{
			AccessLabel: a.Name,
		}}
		l.Info(fmt.Sprintf("create cilium network policy %s/%s", om.Namespace, om.Name))
		cnp := &ciliumv2.CiliumNetworkPolicy{ObjectMeta: om, Specs: cnprs}
		controllerutil.SetOwnerReference(a, cnp, r.Scheme())
		err := r.Create(ctx, cnp)
		if err != nil {
			return failure(err)
		}
	} else if !reflect.DeepEqual(cnp.Specs, cnprs) {
		l.Info(fmt.Sprintf("update cilium network policy %s/%s", cnp.Namespace, cnp.Name))
		cnp.Specs = cnprs
		err := r.Update(ctx, cnp)
		if err != nil {
			return failure(err)
		}
	}

	l.Info("set status")
	a.Status.CurrentSpec = &a.Spec
	err = r.Update(ctx, a)
	if err != nil {
		return failure(err)
	}

	return success()
}
