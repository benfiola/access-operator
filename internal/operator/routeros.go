package operator

import (
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"

	"github.com/go-routeros/routeros/v3"
)

// RouterOsClient is the public interface of the [routerOsClient]
type RouterOsClient interface {
	Delete(namespace string, name string) error
	Sync(namespace string, name string, ai AccessInfo) error
}

// routerOsClient is a wrapper around [routeros.Client] that performs access-operator specific functionality
type routerOsClient struct {
	address  string
	logger   *slog.Logger
	password string
	username string
}

// RouterOsClientOpts are options used to construct a [routerOsClient]
type RouterOsClientOpts struct {
	Address  string
	Logger   *slog.Logger
	Password string
	Username string
}

// Constructs a new [routerOsClient] using the provider [RouterOsClientOpts]
func NewRouterOsClient(opts *RouterOsClientOpts) (RouterOsClient, error) {
	l := opts.Logger
	if l == nil {
		l = slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	}

	return &routerOsClient{
		address:  opts.Address,
		logger:   l,
		password: opts.Password,
		username: opts.Username,
	}, nil
}

// Performs a primitive 'add' operation with the given resource path and payload.
// Returns an error if the operation fails.
func (roc *routerOsClient) add(r string, d map[string]string) error {
	c, err := roc.dial()
	if err != nil {
		return err
	}
	defer c.Close()

	nd := map[string]string{}
	for k, v := range d {
		nd[k] = v
	}
	cmd := []string{fmt.Sprintf("%s/add", r)}
	cmd = append(cmd, roc.toAttributeWords(nd)...)
	_, err = c.RunArgs(cmd)
	return err
}

// Helper method to open a connection to routeros and return a [routeros.Client]
// Returns an error if the operation fails.
func (roc *routerOsClient) dial() (*routeros.Client, error) {
	return routeros.Dial(roc.address, roc.username, roc.password)
}

// Comments are used to identify which records belong to specific access-operator resources.
// This method returns this comment identifier string.
func (roc *routerOsClient) getComment(namespace string, name string) string {
	return fmt.Sprintf("access-operator: %s/%s", namespace, name)
}

// Performs a primitive 'list' (i.e., */print detail) operation with the given resource path
// Returns an error if the operation fails.
func (roc *routerOsClient) printDetail(r string) ([]map[string]string, error) {
	result := []map[string]string{}
	c, err := roc.dial()
	if err != nil {
		return result, err
	}
	defer c.Close()

	re, err := c.Run(fmt.Sprintf("%s/print", r), "detail")
	if err != nil {
		return result, err
	}

	for _, s := range re.Re {
		result = append(result, s.Map)
	}

	return result, nil
}

// Performs a primitive 'remove' operation with the given resource path and payload
// Returns an error if the operation fails.
func (roc *routerOsClient) remove(r string, d map[string]string) error {
	c, err := roc.dial()
	if err != nil {
		return err
	}
	defer c.Close()

	nd := map[string]string{
		"numbers": d[".id"],
	}
	cmd := []string{fmt.Sprintf("%s/remove", r)}
	cmd = append(cmd, roc.toAttributeWords(nd)...)
	_, err = c.RunArgs(cmd)
	return err
}

// Performs a primitive 'update' (i.e., */set) operation with the given resource path and payload
// Returns an error if the operation fails.
func (roc *routerOsClient) set(r string, d map[string]string) error {
	c, err := roc.dial()
	if err != nil {
		return err
	}
	defer c.Close()

	nd := map[string]string{}
	for k, v := range d {
		nd[k] = v
	}
	nd["numbers"] = nd[".id"]
	delete(nd, ".id")
	cmd := []string{fmt.Sprintf("%s/set", r)}
	cmd = append(cmd, roc.toAttributeWords(nd)...)
	_, err = c.RunArgs(cmd)
	return err
}

// Helper method that converts a payload into an array of 'attribute words' (i.e., an array of '=<key>=<value>' strings)
func (roc *routerOsClient) toAttributeWords(d map[string]string) []string {
	s := []string{}
	for k, v := range d {
		s = append(s, fmt.Sprintf("=%s=%s", k, v))
	}
	return s
}

// Calls '/ip/firewall/address-list/add' with the given payload
// Returns an error if the operation fails.
func (roc *routerOsClient) addFirewallAddressList(f map[string]string) error {
	return roc.add("/ip/firewall/address-list", f)
}

// Calls '/ip/firewall/address-list/print detail'
// Returns an error if the operation fails.
func (roc *routerOsClient) listFirewallAddressLists() ([]map[string]string, error) {
	return roc.printDetail("/ip/firewall/address-list")
}

// Calls '/ip/firewall/address-list/remove' with the given payload
// Returns an error if the operation fails.
func (roc *routerOsClient) removeFirewallAddressList(f map[string]string) error {
	return roc.remove("/ip/firewall/address-list", f)
}

// Calls '/ip/firewall/filter/add' with the given payload
// Returns an error if the operation fails.
func (roc *routerOsClient) addFirewallFilter(f map[string]string) error {
	return roc.add("/ip/firewall/filter", f)
}

// Calls '/ip/firewall/filter/print detail'
// Returns an error if the operation fails.
func (roc *routerOsClient) listFirewallFilters() ([]map[string]string, error) {
	return roc.printDetail("/ip/firewall/filter")
}

// Calls '/ip/firewall/filter/remove' with the given payload
// Returns an error if the operation fails.
func (roc *routerOsClient) removeFirewallFilter(f map[string]string) error {
	return roc.remove("/ip/firewall/filter", f)
}

// Calls '/ip/firewall/filter/set' with the given payload
// Returns an error if the operation fails.
func (roc *routerOsClient) updateFirewallFilter(f map[string]string) error {
	return roc.set("/ip/firewall/filter", f)
}

// Calls '/ip/firewall/nat/add' with the given payload
// Returns an error if the operation fails.
func (roc *routerOsClient) addFirewallNat(f map[string]string) error {
	return roc.add("/ip/firewall/nat", f)
}

// Calls '/ip/firewall/nat/print detail'
// Returns an error if the operation fails.
func (roc *routerOsClient) listFirewallNats() ([]map[string]string, error) {
	return roc.printDetail("/ip/firewall/nat")
}

// Calls '/ip/firewall/nat/remove' with the given payload
// Returns an error if the operation fails.
func (roc *routerOsClient) removeFirewallNat(f map[string]string) error {
	return roc.remove("/ip/firewall/nat", f)
}

// Calls '/ip/firewall/nat/set' with the given payload
// Returns an error if the operation fails.
func (roc *routerOsClient) updateFirewallNat(f map[string]string) error {
	return roc.set("/ip/firewall/nat", f)
}

// Performs cleanup when a [v1.Access] is deleted.
// Deletes created firewall filter rules
// Deletes created firewall nat rules
// Deletes created firewall address lists
// Returns an error if any operations fail
func (roc *routerOsClient) Delete(namespace string, name string) error {
	l := roc.logger.With("resource", fmt.Sprintf("%s/%s", namespace, name))

	// delete firewall filter rules
	fs, err := roc.listFirewallFilters()
	if err != nil {
		return err
	}
	for _, f := range fs {
		if f["comment"] != roc.getComment(namespace, name) {
			continue
		}
		l.Info(fmt.Sprintf("delete filter (id: %s)", f[".id"]))
		err := roc.removeFirewallFilter(f)
		if err != nil {
			return err
		}
	}

	// delete firewall address lists
	als, err := roc.listFirewallAddressLists()
	if err != nil {
		return err
	}
	for _, al := range als {
		if al["comment"] != roc.getComment(namespace, name) {
			continue
		}
		l.Info(fmt.Sprintf("delete address list (id: %s)", al[".id"]))
		err := roc.removeFirewallAddressList(al)
		if err != nil {
			return err
		}
	}

	// delete firewall nat
	ns, err := roc.listFirewallNats()
	if err != nil {
		return err
	}
	for _, n := range ns {
		if n["comment"] != roc.getComment(namespace, name) {
			continue
		}
		l.Info(fmt.Sprintf("delete nat (id: %s)", n[".id"]))
		err := roc.removeFirewallNat(n)
		if err != nil {
			return err
		}
	}
	return nil
}

// natKey is used to map routeros nat rules to access information
// See: [routerOsClient.Sync]
type natKey struct {
	FromPort string
	ToIp     string
	ToPort   string
	Protocol string
}

// Helper method that returns a string representation of a [natKey]
func (k natKey) String() string {
	return fmt.Sprintf("%s/%s -> %s:%s/%s", k.FromPort, k.Protocol, k.ToIp, k.ToPort, k.Protocol)
}

// Synchronizes a [v1.Access] resource with a routeros device
// Creates firewall filter rules
// Creates firewall nat rules
// Creates firewall address lists
// Returns an error if any operations fail
func (roc *routerOsClient) Sync(namespace string, name string, ai AccessInfo) error {
	l := roc.logger.With("access", name)

	// sync firewall address lists
	aln := fmt.Sprintf("access-operator/%s/%s", namespace, name)
	cm := map[string]bool{}
	for _, c := range ai.FromCidrs {
		cm[c] = true
	}
	als, err := roc.listFirewallAddressLists()
	if err != nil {
		return err
	}
	for _, al := range als {
		if al["comment"] != roc.getComment(namespace, name) {
			// ignore - address list does not belong to access
			continue
		}
		// routeros strips /32 off the end of addresses
		if !strings.Contains(al["address"], "/") {
			al["address"] = fmt.Sprintf("%s/32", al["address"])
		}
		if _, ok := cm[al["address"]]; !ok {
			// address list address is not member of access - clean up
			l.Info(fmt.Sprintf("remove cidr: %s (id: %s)", al["address"], al[".id"]))
			err := roc.removeFirewallAddressList(al)
			if err != nil {
				return err
			}
		}
		// address list address is still member of access - delete from map
		delete(cm, al["address"])
		continue
	}
	for c := range cm {
		l.Info(fmt.Sprintf("add cidr: %s", c))
		err := roc.addFirewallAddressList(map[string]string{
			"address": c,
			"comment": roc.getComment(namespace, name),
			"list":    aln,
		})
		if err != nil {
			return err
		}
	}

	// sync firewall filter rules
	fs, err := roc.listFirewallFilters()
	if err != nil {
		return err
	}
	var ff map[string]string
	i := 0
	for fi, f := range fs {
		// find first firewall filter rule
		// delete subsequent matching firewall filter rules
		if f["comment"] == roc.getComment(namespace, name) {
			if (ff != nil) || (f["action"] != "accept" || f["chain"] != "input" || f["src-address-list"] != aln) {
				l.Info(fmt.Sprintf("delete existing filter (id: %s)", f[".id"]))
				err := roc.removeFirewallFilter(f)
				if err != nil {
					return err
				}
				continue
			}
			ff = f
		}

		// find index of last 'input' filter chain rule
		if f["chain"] == "input" {
			i = fi
		}
	}
	if ff == nil {
		l.Info(fmt.Sprintf("add filter (before index: %d)", i))
		d := map[string]string{
			"action":           "accept",
			"chain":            "input",
			"comment":          roc.getComment(namespace, name),
			"src-address-list": aln,
		}
		if len(fs) != 0 {
			d["place-before"] = strconv.Itoa(i)
		}
		err := roc.addFirewallFilter(d)
		if err != nil {
			return err
		}
	}

	// sync nat rules
	nm := map[natKey]bool{}
	for _, r := range ai.Rules {
		nm[natKey{FromPort: strconv.Itoa(r.FromPort), Protocol: strings.ToLower(r.Protocol), ToIp: r.ToIp, ToPort: strconv.Itoa(r.FromPort)}] = true
	}
	ns, err := roc.listFirewallNats()
	if err != nil {
		return err
	}
	for _, n := range ns {
		if n["comment"] != roc.getComment(namespace, name) {
			// ignore - nat does not belong to access
			continue
		}
		k := natKey{FromPort: n["dst-port"], ToIp: n["to-addresses"], ToPort: n["to-ports"], Protocol: n["protocol"]}
		if _, ok := nm[k]; !ok || (n["action"] != "dst-nat" || n["chain"] != "dstnat") {
			// nat rule is stale - remove
			l.Info(fmt.Sprintf("remove nat: %s (id: %s)", k.String(), n[".id"]))
			err := roc.removeFirewallNat(n)
			if err != nil {
				return err
			}
			continue
		}
		// nat rule is valid - remove from map
		delete(nm, k)
		continue
	}
	for k := range nm {
		l.Info(fmt.Sprintf("add nat: %s", k.String()))
		err := roc.addFirewallNat(map[string]string{
			"action":           "dst-nat",
			"chain":            "dstnat",
			"comment":          roc.getComment(namespace, name),
			"protocol":         k.Protocol,
			"src-address-list": aln,
			"dst-port":         k.FromPort,
			"to-addresses":     k.ToIp,
			"to-ports":         k.ToPort,
		})
		if err != nil {
			return err
		}
	}

	return nil
}
