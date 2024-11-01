# Cloudflare

DNS record *must* be DNS only (and not proxied through cloudflare).

DNS record *must* have low TTL (to account for xfinity ip address changes).

# RouterOS

## Address List

```
list=testing address=34.127.16.140 creation-time=2024-10-22 21:20:36 dynamic=no
```

## Firewall Filter

```
chain=input action=accept src-address-list=testing
```

## NAT

Use 'dst-port', not 'src-port'.  (The client is trying to connect to the dst-port - the src-port is the opened port on the client-side).

```
chain=dstnat action=dst-nat to-addresses=192.168.33.10 to-ports=80 protocol=tcp src-address-list=testing dst-port=80
```

# Cluster

Should operator create and manage load balancer/ingress resources?  

Rather, accept references to existing services/ingresses and simply copy their specs, BUT:

- If service, change type to LoadBalancer
- If ingress, change 'host' in rules to that in Access?

## CiliumClusterwideNetworkPolicy

For ingress, rules should target ingress (not the individual application).

```
apiVersion: cilium.io/v2
kind: CiliumClusterwideNetworkPolicy
metadata:
  name: testing-ingress
  namespace: testing
specs:
  - endpointSelector:
      matchExpressions:
        - key: reserved:ingress
          operator: Exists
    ingress:
      - fromCIDR:
          - 34.127.16.140/32
        toPorts:
          - ports:
              - port: "80"
                protocol: TCP
    egress:
      - toEndpoints:
          - matchLabels:
              io.kubernetes.pod.namespace: testing
              app: nginx
```