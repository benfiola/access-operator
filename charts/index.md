# Helm Charts

This is the helm charts repository for the [external-access-operator](https://github.com/benfiola/external-access-operator) project.

## Usage

You can add this repository using the `helm` CLI:

```shell
# add the repository
helm repo add external-access-operator https://benfiola.github.io/external-access-operator/charts
# search the repository for installable charts
helm search repo external-access-operator
```

## Charts

- [crds](./README-crds.md)
- [operator](./README-operator.md)
