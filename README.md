Test something
# `ec` a command line client for HACBS Enterprise Contract

The `ec` tool is used to evaluate Enterprise Contract policies for Software
Supply Chain. Various sub-commands can be used to assert facts about an artifact
such as:
  * Container image signature
  * Container image build attestation signature
  * Evaluating enterprise [contract policies][pol] over the build attestation
  * Artifact authorization (sign-off)

Consult the [documentation][docs] for available sub-commands, descriptions and
examples of use.

## Building

Run `make build` from the root directory and use the `dist/ec` executable, or
run `make dist` to build for all supported architectures.

## Testing

Run `make test` to run the unit tests, and `make acceptance` to run the
acceptance tests.

## Linting

Run `make lint` to check for linting issues, and `make lint-fix` to fix linting
issues (formatting, import order, ...).

## Demo

Run `hack/demo.sh` to evaluate the policy against images that have been
built ahead of time. Or use `hack/test-builds.sh hacbs` from the
https://github.com/redhat-appstudio/build-definitions/ repository with
the Tekton Chains controller from the `poc-tep-84` branch, e.g. via the
image built here: https://github.com/hacbs-contract/chains/pkgs/container/chains%2Fcontroller/?tag=poc-tep-84

[pol]: https://github.com/hacbs-contract/ec-policies/
[docs]: https://hacbs-contract.github.io/ec-policies/
