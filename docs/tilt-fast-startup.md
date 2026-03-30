# Tilt Fast Startup Mode

## Overview

Fast startup mode allows developers to skip automatic test execution during
Tilt's initial load, significantly reducing startup time for faster development
iteration.

## Motivation

During normal Tilt startup, the `test` local_resource automatically runs the full test suite (`make test`) which includes:

- Running all unit and integration tests
- Generating coverage reports
- Race condition detection

While comprehensive testing is valuable, this can add 30-60 seconds (or more) to Tilt's startup time. For developers who:

- Are iterating quickly on code changes
- Want to start the application immediately
- Plan to run tests manually when ready

This delay can slow down the development workflow.

## Usage

### Enable Fast Startup Mode

```bash
export TILT_FAST_STARTUP=true
tilt up
```

When enabled, you'll see a banner message:

```text
========================================
🚀 Meridian Development Environment
========================================

⚡ FAST STARTUP MODE ENABLED
   Tests skipped on initial load (trigger manually: 'tilt trigger test')

Services:
...
```

### Disable Fast Startup Mode (Default)

```bash
unset TILT_FAST_STARTUP
tilt up
```

Or explicitly:

```bash
export TILT_FAST_STARTUP=false
tilt up
```

### Running Tests Manually

When fast startup mode is enabled, tests are not automatically run on startup. You can trigger them
manually in several ways:

#### Via Tilt CLI

```bash
tilt trigger test
```

#### Via Tilt UI

1. Open <http://localhost:10350>
2. Navigate to the `test` resource under the "quality" label
3. Click the trigger button

#### Via Make Command

```bash
make test
```

## How It Works

The fast startup mode is controlled by the `TILT_FAST_STARTUP` environment variable:

1. **Tiltfile reads the environment variable** (line 36):

   ```python
   fast_startup = os.getenv('TILT_FAST_STARTUP', 'false').lower() == 'true'
   ```

2. **Test resource `auto_init` is conditionally set** (line 780):

   ```python
   local_resource(
     'test',
     cmd='make test',
     auto_init=False if fast_startup else True,  # Skip on startup in fast mode
     ...
   )
   ```

3. **Startup banner reflects the mode** (lines 888-891):

   ```python
   fast_startup_msg = """
   ⚡ FAST STARTUP MODE ENABLED
      Tests skipped on initial load (trigger manually: 'tilt trigger test')
   """ if fast_startup else ""
   ```

## When to Use Fast Startup Mode

### Use Fast Startup When

- Iterating quickly on new features
- Debugging issues and restarting Tilt frequently
- Working on infrastructure/config changes
- You have external CI running tests on PR

### Use Normal Mode When

- Starting a new work session (want full validation)
- Making critical changes to core functionality
- Working without CI/PR workflow
- You prefer continuous test feedback

## Trade-offs

| Aspect | Fast Startup | Normal Mode |
|--------|--------------|-------------|
| Startup Time | 10-20 seconds faster | Slower (includes test execution) |
| Immediate Feedback | Application ready sooner | Tests run automatically |
| Test Coverage | Manual trigger required | Automatic on startup |
| Best For | Rapid iteration | Comprehensive validation |

## Configuration

The fast startup mode is configured in the `Tiltfile`:

```python
# Fast startup mode - skip tests on initial load for faster development iteration
# Set TILT_FAST_STARTUP=true to skip automatic test execution
# Tests can still be manually triggered via 'tilt trigger test'
fast_startup = os.getenv('TILT_FAST_STARTUP', 'false').lower() == 'true'
```

No additional configuration files or dependencies are required.

## Environment Variable Reference

| Variable | Values | Default | Description |
|----------|--------|---------|-------------|
| `TILT_FAST_STARTUP` | `true`, `false` | `false` | Enable/disable fast startup mode |

The variable is case-insensitive (`True`, `TRUE`, `true` all work).

## Examples

### Shell Profile Setup

Add to `~/.bashrc`, `~/.zshrc`, or `~/.profile`:

```bash
# Enable Tilt fast startup by default
export TILT_FAST_STARTUP=true
```

### Project-Specific `.envrc` (direnv)

Create `.envrc` in the project root:

```bash
# Meridian Tilt configuration
export TILT_FAST_STARTUP=true
```

Then run `direnv allow`.

### One-Time Override

```bash
# Enable for this session only
TILT_FAST_STARTUP=true tilt up

# Explicitly disable (override shell profile)
TILT_FAST_STARTUP=false tilt up
```

## Related Resources

- [Tilt Local Resources](https://docs.tilt.dev/local_resource.html)
- [Tilt Trigger Mode](https://docs.tilt.dev/manual_update_control.html)
- [ADR-0006: Tilt Local Development](./adr/0006-tilt-local-development.md)
