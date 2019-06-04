This directory can be used to override the default configuration from
[test-config.sh](../test-config.sh). Just create one or more files
ending in `.sh`, set variables in those files and then those variables
will override the defaults.

The files are sourced in a bash shell and thus can use arbitrary shell
constructs like `if/else/fi`.

To set defaults that can still be overridden by environment variables,
use this idiom:

``` shell
: ${TEST_DEVICEMODE:=direct}
```
