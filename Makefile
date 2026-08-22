.PHONY: test coverage test-unit coverage-html clean-coverage

test test-unit:
	./scripts/test.sh

coverage: coverage-html

coverage-html:
	./scripts/test.sh --coverage

clean-coverage:
	rm -rf coverage
