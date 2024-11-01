# access-operator

This repo implements a specialized operator that secures access to resources hosted within my [homelab](https://github.com/benfiola/homelab).
It accomplishes this through a combination of custom resources, router firewalls, Cilium network policies and a public authorization frontend.

## Resources

Currently, this operator manages the following resources:

- Definitions of which resources to expose (`AccessClaim`)
- Exposed resources (`Access`)

Examples of these resources can be found [here](./dev/manifests/resources).

## Installation

Use [helm](https://helm.sh/) to install the operator:

```shell
# add the access-operator helm chart repository
helm repo add access-operator https://benfiola.github.io/access-operator/charts
# deploy the crds
helm install access-operator-crds access-operator/crds
# deploy the operator
helm install access-operator-operator access-operator/operator
```

Documentation for these helm charts can be found [here](https://benfiola.github.io/access-operator/).

### Image

The operator is hosted on docker hub and can be found at [docker.io/benfiola/access-operator](https://hub.docker.com/r/benfiola/access-operator).

## Configuration

The operator itself consists of two components: a controller and a server.

### Controller (via the `operator` subcommand)

The controller manages custom resources, creates owned `corev1.Service`, `networkingv1.Ingress` and `ciliumv2.CiliumNetworkPolicy` resources, synchronizes firewall rules with a connected RouterOS device and synchronizes DNS records with Cloudflare.

| CLI                     | Env                                   | Default          | Description                                                                 |
| ----------------------- | ------------------------------------- | ---------------- | --------------------------------------------------------------------------- |
| _--cloudflare-token_    | _ACCESS_OPERATOR_CLOUDFLARE_TOKEN_    |                  | Synchronizes current IP address with exposed service DNS records            |
| _--kubeconfig_          | _ACCESS_OPERATOR_KUBECONFIG_          | `<incluster>`    | Defines a kubernetes configuration file path used to connect to a cluster   |
| _--health-bind-address_ | _ACCESS_OPERATOR_HEALTH_BIND_ADDRESS_ | `127.0.0.1:8888` | Defines the bind address for health and readiness probes                    |
| _--routeros-address_    | _ACCESS_OPERATOR_ROUTEROS_ADDRESS_    |                  | Defines the address to the RouterOS API used to manage firewall rules       |
| _--routeros-password_   | _ACCESS_OPERATOR_ROUTEROS_PASSWORD_   |                  | Defines the password of the user used to authenticate with the RouterOS API |
| _--routeros-username_   | _ACCESS_OPERATOR_ROUTEROS_USERNAME_   |                  | Defines the username of the user used to authenticate with the RouterOS API |

### Server (via the `server` subcommand)

The server provides a simple HTTP API to enumerate exposed services and authorize incoming users with these exposed services.

| CLI              | Env                            | Default          | Description                                                               |
| ---------------- | ------------------------------ | ---------------- | ------------------------------------------------------------------------- |
| _--bind-address_ | _ACCESS_OPERATOR_BIND_ADDRESS_ | `127.0.0.1:8080` | Defines the bind address for the server                                   |
| _--kubeconfig_   | _ACCESS_OPERATOR_KUBECONFIG_   | `<incluster>`    | Defines a kubernetes configuration file path used to connect to a cluster |

## RBAC

### Controller

The controller requires a service account with the following RBAC settings:

| Resource                         | Verbs                   | Why                                                            |
| -------------------------------- | ----------------------- | -------------------------------------------------------------- |
| v1/Service                       | Create,Get,List,Patch   | Subresource created and managed during `Access` reconciliation |
| networking.k8s.io/v1/Ingress     | Create,Get,List,Update  | Subresource created and managed during `Access` reconciliation |
| cilium.io/v2/CiliumNetworkPolicy | Create,Get,List, Update | Subresource created and managed during `Access` reconciliation |
| bfiola.dev/v1/AccessClaim        | Create,Get,List,Update  | Required for the operator to manage its own resources          |
| bfiola.dev/v1/Access             | Create,Get,List,Update  | Required for the operator to manage its own resources          |

### Server

The server requires a service account with the following RBAC settings:

| Resource             | Verbs                  | Why                                                                    |
| -------------------- | ---------------------- | ---------------------------------------------------------------------- |
| v1/Secret            | Get                    | Fetches the password referenced by the `Access.Spec.PasswordRef` field |
| bfiola.dev/v1/Access | Create,Get,List,Update | Used to enumerate the exposed services within the cluster              |

## Development

I personally use [vscode](https://code.visualstudio.com/) as an IDE. For a consistent development experience, this project is also configured to utilize [devcontainers](https://containers.dev/). If you're using both - and you have the [Dev Containers extension](https://marketplace.visualstudio.com/items?itemName=ms-vscode-remote.remote-containers) installed - you can follow the [introductory docs](https://code.visualstudio.com/docs/devcontainers/tutorial) to quickly get started.

NOTE: Helper scripts are written under the assumption that they're being executed within a dev container.

### Installing tools

From the project root, run the following to install useful tools. Currently, this includes:

- controller-gen
- crane
- helm
- helm-docs
- kubectl
- minikube

```shell
cd /workspaces/access-operator
make install-tools
```

### Creating a development environment

From the project root, run the following to create a development environment to test the operator with:

```shell
cd /workspaces/access-operator
make dev-env
```

This will:

- Create a new minikube cluster
- Enable the minikube ingress addon
- Apply the [custom resources](./charts/crds)
- Apply the [example resources](./dev/manifests/resources)
- Start the minikube tunnel
- Start a routeros container
- Wait for routeros to be connectable

### Run end-to-end tests

With a development environment deployed, you can run end-to-end operator tests to confirm the operator functions as expected:

```shell
cd /workspaces/access-operator
make e2e-test
```

### Creating a debug script

Copy the [./dev/dev.go.template](./dev/dev.go.template) script to `./dev/dev.go`, then run it to start the operator. `./dev/dev.go` is ignored by git and can be modified as needed to help facilitate local development.

Additionally, the devcontainer is configured with a vscode launch configuration that points to `./dev/dev.go`. You should be able to launch (and attach a debugger to) the webhook via this vscode launch configuration.
