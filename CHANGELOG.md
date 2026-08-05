# Changelog

## [0.3.0](https://github.com/ilijad1/rookery/compare/v0.2.0...v0.3.0) (2026-08-05)


### Features

* **gateway:** advertise every command in the Telegram, Discord and Slack menus ([#95](https://github.com/ilijad1/rookery/issues/95)) ([04cd87d](https://github.com/ilijad1/rookery/commit/04cd87dade746852497e7c9b035000a48a7fcc2f))
* **web/auth:** add sign out, and gate workspace creation on the owner password ([#96](https://github.com/ilijad1/rookery/issues/96)) ([c9f6eea](https://github.com/ilijad1/rookery/commit/c9f6eeabbe0b2387facdfaa58956ce6b387e92c7))
* **web/workspaces:** show each workspace's icon in the picker ([#101](https://github.com/ilijad1/rookery/issues/101)) ([32ef2a2](https://github.com/ilijad1/rookery/commit/32ef2a221770ef92433321c3cab5ac4228dde532))


### Bug Fixes

* **connectors:** scope the OAuth redirect URI to the app, not the service ([#94](https://github.com/ilijad1/rookery/issues/94)) ([1fb352e](https://github.com/ilijad1/rookery/commit/1fb352ee6eb7b72c75b0e53f4027550199108309))
* **gateway:** stop dropping long designer messages and losing detached builds ([#97](https://github.com/ilijad1/rookery/issues/97)) ([cb17098](https://github.com/ilijad1/rookery/commit/cb17098eaf99fa53c656b0559f49fed87efaf9f1))
* **web/auth:** send a signed-out owner to the login screen, not the picker ([#99](https://github.com/ilijad1/rookery/issues/99)) ([8cdf78c](https://github.com/ilijad1/rookery/commit/8cdf78c5c05153e4c74988a738cacae04a3e8ecf))

## [0.2.0](https://github.com/ilijad1/rookery/compare/v0.1.0...v0.2.0) (2026-08-05)


### ⚠ BREAKING CHANGES

* rename to rookery ([#65](https://github.com/ilijad1/rookery/issues/65))

### Features

* **coder:** fifteen drop-in LLM providers and a discoverable base URL ([#81](https://github.com/ilijad1/rookery/issues/81)) ([96940d4](https://github.com/ilijad1/rookery/commit/96940d46ac8e0201697e70b0a7bfc06b58fbc8ee))
* **connectors:** everyday connector tier — nine providers, keyless auth, extractor fix ([#77](https://github.com/ilijad1/rookery/issues/77)) ([c1407d6](https://github.com/ilijad1/rookery/commit/c1407d60a75721060f0bce6f932c0ecbefbbef67))
* **connectors:** everyday connectors waves 2 & 3 — 25 more providers ([#78](https://github.com/ilijad1/rookery/issues/78)) ([0b5afd0](https://github.com/ilijad1/rookery/commit/0b5afd078ca3141d6ba7e48f17bd1cc3ccc9044e))
* **connectors:** per-provider OAuth credential field labels ([#82](https://github.com/ilijad1/rookery/issues/82)) ([f5ce1e9](https://github.com/ilijad1/rookery/commit/f5ce1e993f7f3886cfbf6e767d1b6f7d41314a64))
* **connectors:** wave 4 — thirteen more everyday providers ([#79](https://github.com/ilijad1/rookery/issues/79)) ([f63c98f](https://github.com/ilijad1/rookery/commit/f63c98fd54e36a639a9db2c9d85a16e5996ad42c))
* **memory:** make memory/ the source of truth for workspace identity ([#68](https://github.com/ilijad1/rookery/issues/68)) ([81646aa](https://github.com/ilijad1/rookery/commit/81646aa655d04edaed63c4402dff93816f01ee7f))
* rename to rookery ([#65](https://github.com/ilijad1/rookery/issues/65)) ([da500d6](https://github.com/ilijad1/rookery/commit/da500d689db8c0596dfd996301b01b2cbb422929))
* **web/connections:** verify chat-app linking before reporting success ([#83](https://github.com/ilijad1/rookery/issues/83)) ([ebdc612](https://github.com/ilijad1/rookery/commit/ebdc6128d25a365166243a2915e6745d11193621))
* **web/setup:** chat app onboarding parity and link diagnostics ([#92](https://github.com/ilijad1/rookery/issues/92)) ([ad1ec6e](https://github.com/ilijad1/rookery/commit/ad1ec6e06a7dc5d88ee527a60ff004eea4a705bc))
* **web:** identity source of truth, owner re-auth, and a UI design-system overhaul ([#70](https://github.com/ilijad1/rookery/issues/70)) ([b6970aa](https://github.com/ilijad1/rookery/commit/b6970aa2b0a1e322201c8ed61b6478f905c63e00))


### Bug Fixes

* **connectors:** Google Health replaces Fitbit, drop Zoom, real logos, credential labels ([#80](https://github.com/ilijad1/rookery/issues/80)) ([fd687ba](https://github.com/ilijad1/rookery/commit/fd687badaa7e3c8779538268d30fdf52b469f468))
* **kb:** contain the icon picker's dialog and drop the vault inbox projection ([#72](https://github.com/ilijad1/rookery/issues/72)) ([a8bc039](https://github.com/ilijad1/rookery/commit/a8bc0392d56fa38cc6122e2d7341120482ed1dcc))
* **release:** sign with a cosign bundle instead of deprecated flags ([#62](https://github.com/ilijad1/rookery/issues/62)) ([8016b11](https://github.com/ilijad1/rookery/commit/8016b11bc14799d3ce63b2eb5cf0c02fe8be5c25))
* **release:** stop release-please tagging with a component prefix ([#63](https://github.com/ilijad1/rookery/issues/63)) ([bd83b12](https://github.com/ilijad1/rookery/commit/bd83b122b1d448e31d2c2c1b495343d00277dd72))
* **web/chat:** drop the Active/Stopped chips and the Stop/Resume button ([#74](https://github.com/ilijad1/rookery/issues/74)) ([314acec](https://github.com/ilijad1/rookery/commit/314acec9f01e22335082c698993e365af835278f))
* **web/connections:** one bot per workspace, and make Disconnect actually stop it ([#93](https://github.com/ilijad1/rookery/issues/93)) ([b78e1c1](https://github.com/ilijad1/rookery/commit/b78e1c1d81899cc31799d2154a4691fa3884b9d8))
* **web/connections:** stop the browser autofilling search boxes ([#76](https://github.com/ilijad1/rookery/issues/76)) ([78db966](https://github.com/ilijad1/rookery/commit/78db9667011dbccab6e1df3989f82387852e1a31))
* **web/ui:** call the /api/v1-prefixed backup endpoints ([#64](https://github.com/ilijad1/rookery/issues/64)) ([1ec4130](https://github.com/ilijad1/rookery/commit/1ec4130099f27d4a0659e304f020cb753c4343d7))
* **web:** provider logos, core-skill crash, and KB editor affordances ([#91](https://github.com/ilijad1/rookery/issues/91)) ([928ea14](https://github.com/ilijad1/rookery/commit/928ea14fedc276d51d32d5fd86719c216dffe992))
* **websearch:** report the engine that actually served, verify keys on save ([#75](https://github.com/ilijad1/rookery/issues/75)) ([cdf2f75](https://github.com/ilijad1/rookery/commit/cdf2f75ee13ab3d0089a65525f25443521a2b76d))
* **web:** second UI polish round — chrome scale, KB folder labels, inbox badge ([#71](https://github.com/ilijad1/rookery/issues/71)) ([72b0701](https://github.com/ilijad1/rookery/commit/72b0701490c712e85572bfe51da93d60466370bf))


### Documentation

* add README, Apache-2.0 LICENSE and the Rookery favicon ([#67](https://github.com/ilijad1/rookery/issues/67)) ([0580e75](https://github.com/ilijad1/rookery/commit/0580e75b3b9e8a3c0e2afdfed54920216f598c0e))

## 0.1.0 (2026-07-29)

The first tagged release. Everything before this point is pre-release history
and is deliberately not itemised here.

### Features

* **backup:** owner-level backup and restore ([#55](https://github.com/ilijad1/simple-agents-v2/issues/55))
* **services:** make OAuth redirect setup reliable and self-diagnosing ([#56](https://github.com/ilijad1/simple-agents-v2/issues/56))
