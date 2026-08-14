# Changelog

## [0.2.0](https://github.com/rookery-ai/rookery/compare/v0.1.4...v0.2.0) (2026-08-14)


### Features

* **cli:** add rookery upgrade and rookery uninstall ([#202](https://github.com/rookery-ai/rookery/issues/202)) ([2b0c799](https://github.com/rookery-ai/rookery/commit/2b0c799086b10d8b307f5ca6a3c513851f70c00b))
* **setup:** end onboarding in one action, and teach chat the product ([#203](https://github.com/rookery-ai/rookery/issues/203)) ([e112d2f](https://github.com/rookery-ai/rookery/commit/e112d2f2e5c61c5f5cdc9754828a3e9dd5df4ffb))


### Bug Fixes

* **coder:** stop the settings form capping every workspace at two minutes ([#200](https://github.com/rookery-ai/rookery/issues/200)) ([4761f56](https://github.com/rookery-ai/rookery/commit/4761f56dcdc403d525d03f51a869ed05788b4aee))
* **config:** derive the database path from a yaml-configured data dir ([#198](https://github.com/rookery-ai/rookery/issues/198)) ([bf66c98](https://github.com/rookery-ai/rookery/commit/bf66c98262c6e633186211495e949eeea828f8ab))


### Refactoring

* **config:** rename ROOKERY_CLAUDE_BIN to ROOKERY_CODER_BIN ([#201](https://github.com/rookery-ai/rookery/issues/201)) ([5d282b6](https://github.com/rookery-ai/rookery/commit/5d282b6462e2c84c69174b39ae5f565ecdbee00f))

## [0.1.4](https://github.com/rookery-ai/rookery/compare/v0.1.3...v0.1.4) (2026-08-14)


### Bug Fixes

* **agentdesigner:** route build results to the surface that owns the session ([#184](https://github.com/rookery-ai/rookery/issues/184)) ([a06242a](https://github.com/rookery-ai/rookery/commit/a06242a1f391453fd54d37d9d5097c75a6c81069))
* **backup:** report file-close failures instead of losing data silently ([#191](https://github.com/rookery-ai/rookery/issues/191)) ([aabe668](https://github.com/rookery-ai/rookery/commit/aabe66874aa675d5af90a2dfef906e8cd221e79d))
* **convert:** block javascript, vbscript and data URLs case-insensitively ([#192](https://github.com/rookery-ai/rookery/issues/192)) ([b60abcd](https://github.com/rookery-ai/rookery/commit/b60abcd4f15665e67ae06eb540cb02a3568aedf4))
* **skillstore:** contain zip entries with a real path check ([#194](https://github.com/rookery-ai/rookery/issues/194)) ([b681ba6](https://github.com/rookery-ai/rookery/commit/b681ba653be1494dcbc6d28c76ce352db1f61bea))
* **web/kb:** escape backslashes when serializing an image src and title ([#193](https://github.com/rookery-ai/rookery/issues/193)) ([d5575c4](https://github.com/rookery-ai/rookery/commit/d5575c4b9d81e8973882d7dec381e0d01d14fd90))


### Refactoring

* **web/kb:** drop a dead negation in the file tree's drop handler ([#195](https://github.com/rookery-ai/rookery/issues/195)) ([b7c30f4](https://github.com/rookery-ai/rookery/commit/b7c30f40293ae135ef1c3458cefdea5ef104a0a9))

## [0.1.3](https://github.com/rookery-ai/rookery/compare/v0.1.2...v0.1.3) (2026-08-13)


### Documentation

* state the connector counts as 100+ rather than exactly ([#188](https://github.com/rookery-ai/rookery/issues/188)) ([7ae9cc4](https://github.com/rookery-ai/rookery/commit/7ae9cc4912075b442cdd0b36d95963c4dfd5b2dc))

## [0.1.2](https://github.com/rookery-ai/rookery/compare/v0.1.1...v0.1.2) (2026-08-13)


### Documentation

* rebuild the README around three generated images ([#186](https://github.com/rookery-ai/rookery/issues/186)) ([11d75a4](https://github.com/rookery-ai/rookery/commit/11d75a43e6a0c7c30ce4a93ffb1226a07ae61bcb))

## [0.1.1](https://github.com/rookery-ai/rookery/compare/v0.1.0...v0.1.1) (2026-08-13)


### Bug Fixes

* **ci:** stamp the container image with a build date and a bare version ([#181](https://github.com/rookery-ai/rookery/issues/181)) ([518ee90](https://github.com/rookery-ai/rookery/commit/518ee908efe06d8f1b5ef62a18fce034146b8f21))


### Documentation

* correct ci-setup after publication ([#182](https://github.com/rookery-ai/rookery/issues/182)) ([0ab93cd](https://github.com/rookery-ai/rookery/commit/0ab93cd8468952ad5f75ee6d6cae47e39a434e8e))
