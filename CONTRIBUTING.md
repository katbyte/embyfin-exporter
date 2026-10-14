# Contributing

[docs/development.md](docs/development.md) says how to build and test. Run `make check-all` before sending a change: it is the build, the tests, the live tests and every linter.

A change that adds or renames a reading also updates [docs/readings.md](docs/readings.md) and the saved page, `lib/readings/testdata/page.txt`.

## Releases

Changes collect under an `Unreleased` heading at the top of [CHANGELOG.md](CHANGELOG.md). A version is tagged when the batch is worth upgrading for.

Breaking changes, such as a renamed reading or setting, are grouped into one release rather than shipped one at a time.

Pushing a tag `vX.Y.Z` publishes the release: the Docker image, the ready-built programs and the Homebrew formula.
