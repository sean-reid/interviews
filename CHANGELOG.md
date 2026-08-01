# Changelog

## [0.4.0](https://github.com/sean-reid/interviews/compare/v0.3.0...v0.4.0) (2026-08-01)


### Features

* generate and track a take-home like a session ([#3](https://github.com/sean-reid/interviews/issues/3)) ([952be73](https://github.com/sean-reid/interviews/commit/952be734a2a6873f67ca6350a4c37bdd5ae47bcd))

## [0.3.0](https://github.com/sean-reid/interviews/compare/v0.2.0...v0.3.0) (2026-08-01)


### Features

* add doctor, and make every command describe itself ([#66](https://github.com/sean-reid/interviews/issues/66)) ([59aea37](https://github.com/sean-reid/interviews/commit/59aea3790f911790ef0e0db987f6fc921573d1fc))
* configure where the problems are, and warn when they are stale ([#71](https://github.com/sean-reid/interviews/issues/71)) ([9688437](https://github.com/sean-reid/interviews/commit/9688437eb13bccff263f473da8be3c89bbd25cc2))
* one command to run an interview, and a registry that remembers it ([#64](https://github.com/sean-reid/interviews/issues/64)) ([aac48c5](https://github.com/sean-reid/interviews/commit/aac48c57a4ebed2daaf708a6f3c71f28f401b0e4))
* **session:** forward a compose app onto the fixed port ([#70](https://github.com/sean-reid/interviews/issues/70)) ([ed7e2bc](https://github.com/sean-reid/interviews/commit/ed7e2bcc1d5da80d00416c6d53ac91de29755eb7))
* **session:** give the candidate a URL for the app itself ([#67](https://github.com/sean-reid/interviews/issues/67)) ([1341886](https://github.com/sean-reid/interviews/commit/13418863bfa2a04a847480695cd5d4d06a2374a0))


### Bug Fixes

* keep credentials out of the sheet and the bundle, and grade against one level ([#65](https://github.com/sean-reid/interviews/issues/65)) ([98b8ea7](https://github.com/sean-reid/interviews/commit/98b8ea75385df1bbd70f1a0a4e9f8a01c629052c))
* stop losing the interview evidence ([#61](https://github.com/sean-reid/interviews/issues/61)) ([da7d529](https://github.com/sean-reid/interviews/commit/da7d52994409e47398799315224b5b34d0eae57c))

## [0.2.0](https://github.com/sean-reid/interviews/compare/v0.1.0...v0.2.0) (2026-07-31)


### Features

* deliver system design problems as candidate bundles ([#44](https://github.com/sean-reid/interviews/issues/44)) ([f0fd19e](https://github.com/sean-reid/interviews/commit/f0fd19e0bbbacbb670bda86505b93f8849edef3e))
* widen the variant space for both debugging scenarios ([#48](https://github.com/sean-reid/interviews/issues/48)) ([fbe1bf4](https://github.com/sean-reid/interviews/commit/fbe1bf4fc24339a0faf835380b24c43197bad5c0))


### Bug Fixes

* close eight engine and CLI correctness issues ([#46](https://github.com/sean-reid/interviews/issues/46)) ([b87ff31](https://github.com/sean-reid/interviews/commit/b87ff314632f60733bfd01e31c8ec9a69d0593d2))
* make session start idempotent and verify the listeners came up ([#42](https://github.com/sean-reid/interviews/issues/42)) ([0a1e291](https://github.com/sean-reid/interviews/commit/0a1e2915d6a7bcb6d08b2bb33f31be47a8b0660f))
* run the candidate's shell as the candidate ([#47](https://github.com/sean-reid/interviews/issues/47)) ([7ec7759](https://github.com/sean-reid/interviews/commit/7ec775985b4d82449c3570ac2934a20cde5e7cfc))

## 0.1.0 (2026-07-31)


### Features

* add the aligner ladder take-home ([#32](https://github.com/sean-reid/interviews/issues/32)) ([775c564](https://github.com/sean-reid/interviews/commit/775c5649d01a5bc85633f1cb4185db7c2bc76df1))
* add the content core ([#7](https://github.com/sean-reid/interviews/issues/7)) ([424a656](https://github.com/sean-reid/interviews/commit/424a656d6f173ba5ef7383fcf80467f8ee6c4fd5))
* add the debugging engine ([#9](https://github.com/sean-reid/interviews/issues/9)) ([4f89a98](https://github.com/sean-reid/interviews/commit/4f89a98937296d4dcbf9af4916a10f936450bd16))
* add the eval gate take-home ([#21](https://github.com/sean-reid/interviews/issues/21)) ([03f605e](https://github.com/sean-reid/interviews/commit/03f605ef9d111fe69bd628678d4978e9f22ba888))
* add the grading core ([#11](https://github.com/sean-reid/interviews/issues/11)) ([c51f10a](https://github.com/sean-reid/interviews/commit/c51f10a615b9c8d9b0a093d02380effc48edc759))
* add the interviews CLI skeleton ([4bc5352](https://github.com/sean-reid/interviews/commit/4bc53522aa2b291d917e6828cab80dafed9aa396))
* add the orbit-shop kubernetes scenario ([#17](https://github.com/sean-reid/interviews/issues/17)) ([3fbd9f4](https://github.com/sean-reid/interviews/commit/3fbd9f4ec95604e4b4abe36ec8a5635822c55960))
* add the pipeline salvage take-home ([#22](https://github.com/sean-reid/interviews/issues/22)) ([a998872](https://github.com/sean-reid/interviews/commit/a9988725e795cf8f2e409222f1e05953f0fe2fcd))
* add the red-team calibration harness ([#13](https://github.com/sean-reid/interviews/issues/13)) ([4892060](https://github.com/sean-reid/interviews/commit/4892060f13ec983fa9ce7213eac8e3188aaed0a1))
* add the relay compose scenario ([#18](https://github.com/sean-reid/interviews/issues/18)) ([ce49729](https://github.com/sean-reid/interviews/commit/ce497292179de0708df54ce77a6d29e2b1061ddc))
* add the session stack ([#16](https://github.com/sean-reid/interviews/issues/16)) ([8a7c65c](https://github.com/sean-reid/interviews/commit/8a7c65c480f7ac4eba0c7ccdba09e0dd59313b8a))
* add the system design interview type ([#12](https://github.com/sean-reid/interviews/issues/12)) ([d98e06f](https://github.com/sean-reid/interviews/commit/d98e06f457bed1f2d01fba812658de6a462301c2))
* add the take-home bundle engine ([#14](https://github.com/sean-reid/interviews/issues/14)) ([567771d](https://github.com/sean-reid/interviews/commit/567771d28a7d8e8ae76367f2062d875a109d7bc4))
* add the windowed stream store take-home ([#20](https://github.com/sean-reid/interviews/issues/20)) ([8ecc91d](https://github.com/sean-reid/interviews/commit/8ecc91d6533163d13be81cf7a623d360e90c47bb))
* add two more design problems ([#15](https://github.com/sean-reid/interviews/issues/15)) ([a5687ac](https://github.com/sean-reid/interviews/commit/a5687ac334953d74d73acccd2e3d5bd18cf8c81e))
* expose engine builtins to rendered env files ([#10](https://github.com/sean-reid/interviews/issues/10)) ([7a4155a](https://github.com/sean-reid/interviews/commit/7a4155addceb2711af26f975b3e8ff67cc528ca6))


### Bug Fixes

* close the audit findings that break a live interview ([#28](https://github.com/sean-reid/interviews/issues/28)) ([81d40c0](https://github.com/sean-reid/interviews/commit/81d40c01387d08966850ca92e8344f89c77ed337))
* close three verified leaks in the candidate bundle path ([#23](https://github.com/sean-reid/interviews/issues/23)) ([45696a7](https://github.com/sean-reid/interviews/commit/45696a7a48c92fe4de12e409eaea118a4afa2f43))
