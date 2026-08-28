# Changelog

## [0.10.0](https://github.com/rookery-ai/rookery/compare/v0.9.1...v0.10.0) (2026-08-28)


### Features

* **browser:** read and act on JavaScript-rendered pages ([#265](https://github.com/rookery-ai/rookery/issues/265)) ([99cd586](https://github.com/rookery-ai/rookery/commit/99cd5864502511cc9a7d62ed3b0e2e61de5ff3a2))

## [0.9.1](https://github.com/rookery-ai/rookery/compare/v0.9.0...v0.9.1) (2026-08-24)


### Bug Fixes

* **agentdesigner:** show the design conversation's tool calls ([#263](https://github.com/rookery-ai/rookery/issues/263)) ([107acf9](https://github.com/rookery-ai/rookery/commit/107acf90e53a2cddc45f23748ff96be8ae49cb77))
* **agentdesigner:** stop a build and its dry run writing into the live knowledge base ([#262](https://github.com/rookery-ai/rookery/issues/262)) ([c84d019](https://github.com/rookery-ai/rookery/commit/c84d019e1cd543fca48973653c983ca5adf1a80d))

## [0.9.0](https://github.com/rookery-ai/rookery/compare/v0.8.1...v0.9.0) (2026-08-24)


### Features

* **designers:** give the design conversation read-only tools ([#259](https://github.com/rookery-ai/rookery/issues/259)) ([8524dee](https://github.com/rookery-ai/rookery/commit/8524dee7f31ebfc0c09ab68fd96836f209564a04))

## [0.8.1](https://github.com/rookery-ai/rookery/compare/v0.8.0...v0.8.1) (2026-08-24)


### Bug Fixes

* **runs:** carry cache stats through, and report what a run cost ([#258](https://github.com/rookery-ai/rookery/issues/258)) ([4a432a0](https://github.com/rookery-ai/rookery/commit/4a432a0c44291c082b40a0683b1baefcfa18d202))

## [0.8.0](https://github.com/rookery-ai/rookery/compare/v0.7.0...v0.8.0) (2026-08-24)


### Features

* **runs:** measure prompt caching, and move the activity list to the run log ([#256](https://github.com/rookery-ai/rookery/issues/256)) ([8f38836](https://github.com/rookery-ai/rookery/commit/8f388363cd9f537c52ab5fa35e8066e0b5bf8e8d))

## [0.7.0](https://github.com/rookery-ai/rookery/compare/v0.6.2...v0.7.0) (2026-08-24)


### Features

* **agents:** make agent runs inspectable, and fix four interface defects ([#254](https://github.com/rookery-ai/rookery/issues/254)) ([728df71](https://github.com/rookery-ai/rookery/commit/728df71f2a61a1f72536074c1a1a3ddfdf8d41ee))

## [0.6.2](https://github.com/rookery-ai/rookery/compare/v0.6.1...v0.6.2) (2026-08-24)


### Bug Fixes

* **coder:** tell a truncated reasoning answer apart from an empty one ([#252](https://github.com/rookery-ai/rookery/issues/252)) ([96fb73d](https://github.com/rookery-ai/rookery/commit/96fb73d57522f360a61d9d6f092ba226367f4019))

## [0.6.1](https://github.com/rookery-ai/rookery/compare/v0.6.0...v0.6.1) (2026-08-21)


### Bug Fixes

* **kb:** point a truncated table result at the column count, and reinforce the tools at run time ([#251](https://github.com/rookery-ai/rookery/issues/251)) ([691a52b](https://github.com/rookery-ai/rookery/commit/691a52b2b9080c52462ce9cbb8c5f82453b9c330))
* **vault:** sanitise the error text too, not just the path beside it ([#249](https://github.com/rookery-ai/rookery/issues/249)) ([3784a0b](https://github.com/rookery-ai/rookery/commit/3784a0bf33f28ae27b0e11b91f47a81d9268e82c))

## [0.6.0](https://github.com/rookery-ai/rookery/compare/v0.5.0...v0.6.0) (2026-08-21)


### Features

* **kb:** navigate and compute over big knowledge-base files ([#247](https://github.com/rookery-ai/rookery/issues/247)) ([1e23c62](https://github.com/rookery-ai/rookery/commit/1e23c624579ee8405b9beccabfc6fc4fb3bcf73a))

## [0.5.0](https://github.com/rookery-ai/rookery/compare/v0.4.1...v0.5.0) (2026-08-21)


### Features

* **chat:** durable turns with live progress, agent residue sweep, and table-aware KB retrieval ([#245](https://github.com/rookery-ai/rookery/issues/245)) ([21eb41b](https://github.com/rookery-ai/rookery/commit/21eb41b243df52fd1ff3d29d1683360619ff3979))

## [0.4.1](https://github.com/rookery-ai/rookery/compare/v0.4.0...v0.4.1) (2026-08-20)


### Bug Fixes

* **chat:** stop the agent output-protocol markers leaking into chat replies ([#242](https://github.com/rookery-ai/rookery/issues/242)) ([df3c1fd](https://github.com/rookery-ai/rookery/commit/df3c1fd26129b129143460d5718b385fbf628253))

## [0.4.0](https://github.com/rookery-ai/rookery/compare/v0.3.11...v0.4.0) (2026-08-20)


### Features

* **scheduler:** evaluate cron in the schedule's own timezone ([#241](https://github.com/rookery-ai/rookery/issues/241)) ([3ae05c3](https://github.com/rookery-ai/rookery/commit/3ae05c34ca60c126334cabcecea4263b603682d9))


### Bug Fixes

* **llm:** classify a 404 tool rejection, count late run warnings, bound the state read ([#239](https://github.com/rookery-ai/rookery/issues/239)) ([5ba78eb](https://github.com/rookery-ai/rookery/commit/5ba78eb04a2628d9374a4d12420c5892b1e832d8))

## [0.3.11](https://github.com/rookery-ai/rookery/compare/v0.3.10...v0.3.11) (2026-08-19)


### Bug Fixes

* **agentstate:** give agent state one owner and recover memory written outside the fence ([#237](https://github.com/rookery-ai/rookery/issues/237)) ([f9af2f6](https://github.com/rookery-ai/rookery/commit/f9af2f620ce1b6839b5c2a74485d5e18cc403a70))

## [0.3.10](https://github.com/rookery-ai/rookery/compare/v0.3.9...v0.3.10) (2026-08-19)


### Bug Fixes

* **web/designer:** stop the notice hijacking its own build, unclip the dry run ([#234](https://github.com/rookery-ai/rookery/issues/234)) ([4a86899](https://github.com/rookery-ai/rookery/commit/4a8689952fead66bff027773e90d6757cff6a7ba))

## [0.3.9](https://github.com/rookery-ai/rookery/compare/v0.3.8...v0.3.9) (2026-08-19)


### Bug Fixes

* **web/designer:** show the dry run when a build lands on the Spec tab ([#232](https://github.com/rookery-ai/rookery/issues/232)) ([11a07de](https://github.com/rookery-ai/rookery/commit/11a07de634c0802053f6ea85341d09b0f7685a82))

## [0.3.8](https://github.com/rookery-ai/rookery/compare/v0.3.7...v0.3.8) (2026-08-18)


### Bug Fixes

* **agentdesigner:** run the built agent so the review shows real output ([#227](https://github.com/rookery-ai/rookery/issues/227)) ([35d7f13](https://github.com/rookery-ai/rookery/commit/35d7f13c5a988d216911b171aaa975840ac9c271))

## [0.3.7](https://github.com/rookery-ai/rookery/compare/v0.3.6...v0.3.7) (2026-08-18)


### Bug Fixes

* **coder:** budget tool-loop turns on progress, and never deliver tool-call scaffolding ([#224](https://github.com/rookery-ai/rookery/issues/224)) ([e4b82b9](https://github.com/rookery-ai/rookery/commit/e4b82b9dd2b1c5f0617eef72206112670dacc48c))

## [0.3.6](https://github.com/rookery-ai/rookery/compare/v0.3.5...v0.3.6) (2026-08-17)


### Bug Fixes

* **designer:** move the action bar out of the scrolling transcript ([#222](https://github.com/rookery-ai/rookery/issues/222)) ([7a4a95e](https://github.com/rookery-ai/rookery/commit/7a4a95e61201c80ef4106bc2ca8232d1677df1fa))

## [0.3.5](https://github.com/rookery-ai/rookery/compare/v0.3.4...v0.3.5) (2026-08-17)


### Bug Fixes

* **designer:** never leave a locked composer with no visible buttons ([#220](https://github.com/rookery-ai/rookery/issues/220)) ([b98ebec](https://github.com/rookery-ai/rookery/commit/b98ebec003d627f20d9ebd990f34f3f4badf37ce))

## [0.3.4](https://github.com/rookery-ai/rookery/compare/v0.3.3...v0.3.4) (2026-08-17)


### Bug Fixes

* **agentrunner:** obey [SILENT], fail loudly on empty runs, stop over-binding connections ([#218](https://github.com/rookery-ai/rookery/issues/218)) ([34b1c38](https://github.com/rookery-ai/rookery/commit/34b1c38a3ac2f80e0d34be677f34168518215110))

## [0.3.3](https://github.com/rookery-ai/rookery/compare/v0.3.2...v0.3.3) (2026-08-17)


### Bug Fixes

* **agentdesigner:** stop a finished build silently rebuilding instead of saving ([#216](https://github.com/rookery-ai/rookery/issues/216)) ([fadfcdc](https://github.com/rookery-ai/rookery/commit/fadfcdc4989e9f6b2014f1416441698995c81970))

## [0.3.2](https://github.com/rookery-ai/rookery/compare/v0.3.1...v0.3.2) (2026-08-17)


### Bug Fixes

* **scheduler:** recover interrupted runs, cap the boot catch-up, fix per-connection pragmas ([#214](https://github.com/rookery-ai/rookery/issues/214)) ([10926d1](https://github.com/rookery-ai/rookery/commit/10926d1c8b7c303add1af30296b5f45d9a821e8a))

## [0.3.1](https://github.com/rookery-ai/rookery/compare/v0.3.0...v0.3.1) (2026-08-17)


### Bug Fixes

* **designer:** never show a blank turn, and teach the prompts the inbox ([#211](https://github.com/rookery-ai/rookery/issues/211)) ([e05e822](https://github.com/rookery-ai/rookery/commit/e05e82299cee8a5b5920b2b928f7a65eae21d7b0))
* **docker:** apply base-image security updates in the runtime stage ([#212](https://github.com/rookery-ai/rookery/issues/212)) ([6bccbbc](https://github.com/rookery-ai/rookery/commit/6bccbbc85e948e24a41be2865562814b2f2c3afc))

## [0.3.0](https://github.com/rookery-ai/rookery/compare/v0.2.0...v0.3.0) (2026-08-16)


### Features

* **designer:** offer the build button only when the plan is settled ([#204](https://github.com/rookery-ai/rookery/issues/204)) ([2cfdec0](https://github.com/rookery-ai/rookery/commit/2cfdec0c0d4c3c7557fa8a500726082be6d1e54d))
* **kb:** align text and images, and unfreeze the toolbar's pressed states ([#206](https://github.com/rookery-ai/rookery/issues/206)) ([ef7a96c](https://github.com/rookery-ai/rookery/commit/ef7a96c31ce76bc4053a8ac929ee9fc74b4a8c4c))
* **kb:** lay blocks out in columns ([#207](https://github.com/rookery-ai/rookery/issues/207)) ([f805932](https://github.com/rookery-ai/rookery/commit/f8059321d667b6b3273ea67a0ad159291f626e62))


### Bug Fixes

* **cli:** make backup, upgrade and uninstall behave correctly on Windows ([#210](https://github.com/rookery-ai/rookery/issues/210)) ([2c972ad](https://github.com/rookery-ai/rookery/commit/2c972adefbafcee019c8366d950171142b78724f))
* **kb:** stop the protocol leaking into AI actions, and pick up chat's edits ([#205](https://github.com/rookery-ai/rookery/issues/205)) ([f6a847b](https://github.com/rookery-ai/rookery/commit/f6a847b94221dc752e1f184b92b5459465fb30ea))

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
