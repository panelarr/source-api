# Changelog

## [0.1.1](https://github.com/panelarr/source-api/compare/v0.1.0...v0.1.1) (2026-11-12)


### Features

* **proto:** optional compat_ids on Source and PackageSource ([333bb17](https://github.com/panelarr/source-api/commit/333bb17d70d513ccb8034137a9241320072e0e43))
* **proto:** optional compat_ids on Source and PackageSource ([1ffe197](https://github.com/panelarr/source-api/commit/1ffe197a142f1354420ea06e9fed59084550d209))


### Bug Fixes

* **deps:** build with Go 1.27.4 ([092eddc](https://github.com/panelarr/source-api/commit/092eddceb82aa25cc2050442c515d9a52dc4ba36))

## 0.1.0 (2026-11-09)


### Features

* a jobs capability with a shared job model ([ab7a019](https://github.com/panelarr/source-api/commit/ab7a019ea58ba955fa2bec8f830af5eea1b46ac1))
* a separate image budget in declared limits ([c9dbc17](https://github.com/panelarr/source-api/commit/c9dbc17a06b2cd05f8b90f189df3a60b22d8caf6))
* instance_id in Health, and GetNetworkProfile ([348bc2d](https://github.com/panelarr/source-api/commit/348bc2d21fab4cff2cca5ecb0f9403a40e9ab925))
* **jobs:** a job can say why it can't run now ([fb6ed48](https://github.com/panelarr/source-api/commit/fb6ed48ab471a62e282b69d2ec68b0ab39f55f98))
* package origins, details and reload; per-source health checks ([43548cf](https://github.com/panelarr/source-api/commit/43548cf656c331095f659f46f07f35014417cb1f))
* packages and languages for source plugins ([2ac5461](https://github.com/panelarr/source-api/commit/2ac5461e9f3c8d137dc87055203fa47e5dee0e51))
* **proto:** add GetConfig and SetConfig for plugin-level configuration ([aab3ecf](https://github.com/panelarr/source-api/commit/aab3ecf655dcfa45ba29da2c5f77fa32c5a8aab0))
* **proto:** add panelarr.source.v1 with generated Go and OpenAPI ([69a73ff](https://github.com/panelarr/source-api/commit/69a73ff8323eee5e406df9d6e0186a0706fba2ed))
* **sdk:** extra HTTP routes for standalone plugins ([f62b01a](https://github.com/panelarr/source-api/commit/f62b01a8d67135b333a9eefbba803647fc3dcfc1))
* **sdk:** keep the order of settings and configuration fields ([a3e91cd](https://github.com/panelarr/source-api/commit/a3e91cd5fef6b880164e761043a4bda1ddb09113))
* **sdk:** plugin SDK, clients, httpx, settings and conformance ([dcf0c7d](https://github.com/panelarr/source-api/commit/dcf0c7d5fd4c25f963ffc5405c8c269c4c6d5c1c))


### Bug Fixes

* build with Go 1.27.3 and skip CodeQL while private ([5faeaa6](https://github.com/panelarr/source-api/commit/5faeaa67cf8ab8fa56d501402ed38d8660fada67))
* **conformance:** accept INVALID_ARGUMENT for a malformed source ID in the health-check switch ([4ac0ea1](https://github.com/panelarr/source-api/commit/4ac0ea140c62afc38e4d70360b391afb1c3902b2))
* **deps:** build with Go 1.27.3 ([3c656b6](https://github.com/panelarr/source-api/commit/3c656b65d18ebefeae4b8efe5a40eca8f3d39c55))
