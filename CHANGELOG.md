# Changelog

## [1.5.1](https://github.com/ishioni/ix-csi/compare/1.5.0...1.5.1) (2026-08-09)


### Bug Fixes

* support Grafana dashboard folders ([#25](https://github.com/ishioni/ix-csi/issues/25)) ([410ad1b](https://github.com/ishioni/ix-csi/commit/410ad1be8930704fe90c99a1e8971e13d3039b59))

## [1.5.0](https://github.com/ishioni/ix-csi/compare/1.4.0...1.5.0) (2026-08-09)


### Features

* add Prometheus metrics ([#23](https://github.com/ishioni/ix-csi/issues/23)) ([c0d65ba](https://github.com/ishioni/ix-csi/commit/c0d65bae6a3b51840b6dadefe5d45f5751884df6))
* support templated TrueNAS dataset descriptions ([#21](https://github.com/ishioni/ix-csi/issues/21)) ([eeddd0d](https://github.com/ishioni/ix-csi/commit/eeddd0d8173805091d640a8a580c611ae231b8e1))

## [1.4.0](https://github.com/ishioni/ix-csi/compare/1.3.3...1.4.0) (2026-08-05)


### Features

* add chart-wide log levels ([#20](https://github.com/ishioni/ix-csi/issues/20)) ([44bd8aa](https://github.com/ishioni/ix-csi/commit/44bd8aaa36010ae4f7cca034fcd3145c6501a50b))


### Bug Fixes

* pin Helm chart images by digest ([#18](https://github.com/ishioni/ix-csi/issues/18)) ([dd447dd](https://github.com/ishioni/ix-csi/commit/dd447ddd4d142ba68fc766fd0a9a8857f76973f4))

## [1.3.3](https://github.com/ishioni/ix-csi/compare/1.3.2...1.3.3) (2026-08-04)


### Bug Fixes

* preserve snapshot ownership properties ([#16](https://github.com/ishioni/ix-csi/issues/16)) ([9af07c6](https://github.com/ishioni/ix-csi/commit/9af07c6fe0fef51ba221786ccc2a16db4d5a994c))

## [1.3.2](https://github.com/ishioni/ix-csi/compare/1.3.1...1.3.2) (2026-08-04)


### Bug Fixes

* preserve source volumes with CSI snapshots ([#14](https://github.com/ishioni/ix-csi/issues/14)) ([1710426](https://github.com/ishioni/ix-csi/commit/17104265cc1a742e94ff77346aeaf474671c352f))

## [1.3.1](https://github.com/ishioni/ix-csi/compare/1.3.0...1.3.1) (2026-08-01)


### Bug Fixes

* load NVMe modules in host namespace ([#12](https://github.com/ishioni/ix-csi/issues/12)) ([397b355](https://github.com/ishioni/ix-csi/commit/397b355500c9bd418d170e7d486961ea5b360d81))

## [1.3.0](https://github.com/ishioni/ix-csi/compare/1.2.0...1.3.0) (2026-08-01)


### Features

* make Helm the primary installation method ([#7](https://github.com/ishioni/ix-csi/issues/7)) ([44abdf5](https://github.com/ishioni/ix-csi/commit/44abdf50d5709a47456538b5e702d6c9f55b6040))
* remove the OpenShift operator ([#8](https://github.com/ishioni/ix-csi/issues/8)) ([e1e178c](https://github.com/ishioni/ix-csi/commit/e1e178ce0711c1b34e5b0d74a14f59941ce6c16e))


### Bug Fixes

* use fork image in Helm chart ([#9](https://github.com/ishioni/ix-csi/issues/9)) ([43f96e4](https://github.com/ishioni/ix-csi/commit/43f96e4846be6d5467e9961826a0dd3d52ee6162))


### Documentation

* replace upstream repository URLs ([#11](https://github.com/ishioni/ix-csi/issues/11)) ([1326948](https://github.com/ishioni/ix-csi/commit/1326948b284c99d9c706f3585fe83de3da08ece4))
* use OCI chart in production demo ([4084975](https://github.com/ishioni/ix-csi/commit/4084975faaa784579f4300a886890f59368b3d32))

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
