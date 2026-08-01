# Changelog

## [1.2.0](https://github.com/ishioni/ix-csi/compare/v1.1.2...1.2.0) (2026-08-01)


### Features

* add Helm chart and unified release workflow ([a1e15cf](https://github.com/ishioni/ix-csi/commit/a1e15cfcbc02951de4f36f8a74d4305dd4906e73))
* source sensitive credentials from Secrets ([#3](https://github.com/ishioni/ix-csi/issues/3)) ([08b3ce1](https://github.com/ishioni/ix-csi/commit/08b3ce1226ef7640c758bc24883506eedabe2967))
* support detached snapshots and volume clones ([#5](https://github.com/ishioni/ix-csi/issues/5)) ([2460225](https://github.com/ishioni/ix-csi/commit/246022519b9afbf7ebb9d95acaba61b4c8608c9d))


### Bug Fixes

* implement sparse (thin provisioning) parameter for iSCSI zvols ([216c52f](https://github.com/ishioni/ix-csi/commit/216c52f9013041f70820e273a0e01dfc3b268242))
* make iSCSI CHAP work end to end ([#4](https://github.com/ishioni/ix-csi/issues/4)) ([c3da3bf](https://github.com/ishioni/ix-csi/commit/c3da3bf6e41fe6b8fe2449e2202c5cbc924f79fe))


### Documentation

* update storageclass parameter reference ([8c7e00f](https://github.com/ishioni/ix-csi/commit/8c7e00f9ae1fe8157984e39381d3134e7be5bcf5))
