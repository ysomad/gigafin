# Go Style Guide

Condensed [Uber Go Style Guide](https://github.com/uber-go/guide). Extends
[Effective Go](https://go.dev/doc/effective_go), [Go Common Mistakes](https://go.dev/wiki/CommonMistakes),
[Go Code Review Comments](https://go.dev/wiki/CodeReviewComments). Code must pass `go vet` and the
linters below; run `goimports` on save.

## Guidelines

### Pointers to Interfaces
Almost never use a pointer to an interface. Pass interfaces as values; the underlying data can
still be a pointer. An interface is two fields: a type pointer and a data pointer (a value is
stored via pointer). To let interface methods modify the underlying data, use a pointer receiver.

### Verify Interface Compliance
Assert at compile time for exported types whose API contract requires an interface, types in a
collection implementing the same interface, and other cases where breaking it breaks users.
RHS is the asserted type's zero value: `nil` for pointers/slices/maps, empty struct for structs.

```go
var _ http.Handler = (*Handler)(nil)
var _ http.Handler = LogHandler{}
```

### Receivers and Interfaces
Value-receiver methods are callable on values and pointers. Pointer-receiver methods only on
pointers or [addressable values](https://go.dev/ref/spec#Method_values) (map values are not
addressable: `map[int]S` allows `m[1].Read()` but not `m[1].Write()`; `map[int]*S` allows both).
Likewise, a pointer satisfies an interface even for value-receiver methods, but a value does not
satisfy it for pointer-receiver methods. See [Pointers vs. Values](https://go.dev/doc/effective_go#pointers_vs_values).

### Zero-value Mutexes are Valid
Zero `sync.Mutex`/`sync.RWMutex` is valid: `var mu sync.Mutex`, not `new(sync.Mutex)`.
In a struct used by pointer, make the mutex a named non-pointer field. Never embed it, even on
unexported types — embedding leaks `Lock`/`Unlock` into the API.

```go
type SMap struct {
  mu sync.Mutex

  data map[string]string
}

func (m *SMap) Get(k string) string {
  m.mu.Lock()
  defer m.mu.Unlock()
  return m.data[k]
}
```

### Copy Slices and Maps at Boundaries
Slices and maps hold pointers to shared data.
- **Receiving:** if you store an argument, copy it, or caller mutations leak in.
  ```go
  d.trips = make([]Trip, len(trips))
  copy(d.trips, trips)
  ```
- **Returning:** don't return internal state; return a copy (returning `s.counters` from a
  mutex-guarded method exposes it to data races).
  ```go
  result := make(map[string]int, len(s.counters))
  for k, v := range s.counters {
    result[k] = v
  }
  return result
  ```

### Defer to Clean Up
Use `defer` for files, locks, etc. Manual unlocks are easy to miss with multiple returns. Defer
overhead is tiny; avoid it only when you can prove the function runs in nanoseconds.

```go
p.Lock()
defer p.Unlock()
if p.count < 10 {
  return p.count
}
p.count++
return p.count
```

### Channel Size is One or None
Channels are unbuffered or size 1. Any other size needs heavy scrutiny: how is it chosen, what
stops it filling under load and blocking writers, what happens then. Bad: `make(chan int, 64) //
Ought to be enough`.

### Start Enums at One
Variables default to 0, so start enums at non-zero: `Add Operation = iota + 1`. Exception: when
zero is the desirable default (`LogToStdout LogOutput = iota`).

### Use `"time"` to Handle Time
False assumptions: a day has 24h, an hour 60m, a week 7 days, a year 365 days, [and more](https://infiniteundo.com/post/25326999628/falsehoods-programmers-believe-about-time).
Always use package `time`.
- **Instants:** `time.Time`, and its methods to compare/add/subtract:
  `func isActive(now, start, stop time.Time) bool { return (start.Before(now) || start.Equal(now)) && now.Before(stop) }`
  not `int`s.
- **Periods:** `time.Duration`: `poll(10*time.Second)`, not `poll(10) // seconds or ms?`.
- Same time next calendar day: `t.AddDate(0, 0, 1)`. Exactly 24h later: `t.Add(24 * time.Hour)`.
- **External systems:** use `time.Duration`/`time.Time` where supported — `flag` (via
  `time.ParseDuration`), `encoding/json` (`time.Time` as RFC 3339), `database/sql`
  (`DATETIME`/`TIMESTAMP` if driver supports), `gopkg.in/yaml.v2` (`time.Time` RFC 3339,
  `time.Duration` via `ParseDuration`).
  - No `Duration` support → `int`/`float64` with unit in the name: ``IntervalMillis int `json:"intervalMillis"` ``, not `Interval int`.
  - No `Time` support → `string` formatted as RFC 3339 (`time.RFC3339`, default of
    `Time.UnmarshalText`), unless an alternative is agreed.
- `time` doesn't parse leap-second timestamps ([8728](https://github.com/golang/go/issues/8728)) nor count leap
  seconds in differences ([15190](https://github.com/golang/go/issues/15190)).

### Errors

#### Errors Package
Import `internal/errors`, never stdlib `errors`: a drop-in replacement (`Is`, `As`, `AsType`,
`Join`) without `errors.New`. Errors are constants of its `errors.Error` type
([constant errors](https://dave.cheney.net/2016/04/07/constant-errors)), not `errors.New` vars.

#### Error Types
Choose by: does the caller need to match it (`errors.Is`/`errors.As`)? Is the message static or
dynamic? Propagating a downstream error → see Error Wrapping.

| Matching? | Message | Use                                  |
|-----------|---------|--------------------------------------|
| No        | static  | `errors.Error("...")`                |
| No        | dynamic | `fmt.Errorf`                         |
| Yes       | static  | top-level `const` of `errors.Error`  |
| Yes       | dynamic | custom `error` type                  |

```go
const ErrCouldNotOpen errors.Error = "could not open" // match with errors.Is

type NotFoundError struct{ File string }              // match with errors.As
func (e *NotFoundError) Error() string { return fmt.Sprintf("file %q not found", e.File) }

var notFound *NotFoundError
if errors.As(err, &notFound) { /* handle */ }
```

Exported error vars/types are part of the package's public API.

#### Error Wrapping
On failure, either return the error as-is (no context to add; keeps type and message), or add
context with `fmt.Errorf`:
- `%w` — caller can match the cause. Good default, but callers may rely on it: for known
  vars/types, document and test it as part of the contract.
- `%v` — hides the cause; can switch to `%w` later.

Keep context succinct; no "failed to" (it piles up: `failed to x: failed to y: ...`).

```go
return fmt.Errorf("new store: %w", err) // x: y: new store: the error
```

Once sent to another system, mark it as an error (`err` tag, "Failed" prefix in logs). See
[Don't just check errors, handle them gracefully](https://dave.cheney.net/2016/04/27/dont-just-check-errors-handle-them-gracefully).

#### Error Naming
Global error values: prefix `Err` (exported) or `err` (unexported) — overrides the `_` prefix
rule. Custom error types: suffix `Error` (`NotFoundError`, `resolveError`). Unexported ones can
still be matched inside the package.

#### Handle Errors Once
A caller may: match with `errors.Is`/`As` and branch; log and degrade gracefully if recoverable;
return a well-defined domain error; or return it wrapped/verbatim. Handle each error **once** —
don't log and return (callers up the stack will too → noisy logs).

```go
// Bad
if err != nil {
  log.Printf("Could not get user %q: %v", id, err)
  return err
}

// Good: wrap and return
return fmt.Errorf("get user %q: %w", id, err)

// Good: log and degrade (non-essential op)
if err := emitMetrics(); err != nil {
  log.Printf("Could not emit metrics: %v", err)
}

// Good: match recoverable case, wrap the rest
if errors.Is(err, ErrUserNotFound) {
  tz = time.UTC
} else {
  return fmt.Errorf("get user %q: %w", id, err)
}
```

### Logging
`log/slog`, JSON, one `*slog.Logger` built in `app.Run` and passed through constructors. Use the
`*Context` methods with typed attrs (`slog.Int64("user_id", id)`) so `logx.Handler` adds the
request's `request_id` and `user_id`. Keys are snake_case and mean one thing everywhere:
`user_id`, `import_id`, `account_id`, `category_id`, `operation_id`, `key_id`, `count`, `error`.

| Level | Business (services) | System (app, ui, api, mcp) |
|---|---|---|
| DEBUG | routine CRUD, logout-like noise | config, static assets, 4xx reasons, failed response writes, idle jobs |
| INFO | audit events: users, sign-ins, passwords, sessions, API keys, deletes of accounts and categories, imports, export, backup, delete-all | start, stop, one access line per request, jobs that changed something |
| WARN | rejected API keys | failed sign-ins (fail2ban), forbidden requests, net/http server errors |
| ERROR | never: return the error | 5xx and panics at the boundary, failed jobs |

- Services log after the change is stored, outside the transaction, so a rollback never logs
  success; they always add `user_id`, since jobs and the CLI have no request.
- Never log passwords, tokens, cookies, hashes, amounts, payees, descriptions, account or
  category names, query strings or bodies: log IDs and counts.

### Handle Type Assertion Failures
Single-value assertion panics on wrong type. Always use comma-ok: `t, ok := i.(string)`.

### Don't Panic
Production code must not panic (cascading failures). Return an error and let the caller decide;
`main` prints and exits:

```go
func main() {
  if err := run(os.Args[1:]); err != nil {
    fmt.Fprintln(os.Stderr, err)
    os.Exit(1)
  }
}
```

Panic/recover is not error handling. Panic only on irrecoverable things (nil deref). Exception:
program initialization may panic on fatal startup errors:
`var _statusTemplate = template.Must(template.New("name").Parse("_statusHTML"))`.
In tests use `t.Fatal`/`t.FailNow`, not panic, so the test is marked failed.

### Use go.uber.org/atomic
`sync/atomic` works on raw types, so it's easy to forget atomic access (`return f.running == 1 //
race!`). [go.uber.org/atomic](https://pkg.go.dev/go.uber.org/atomic) hides the raw type and adds
`atomic.Bool`: `running atomic.Bool`; `f.running.Swap(true)`; `f.running.Load()`.

### Avoid Mutable Globals
No mutating globals (values or function pointers); use dependency injection.

```go
// Bad: var _timeNow = time.Now, swapped in tests
type signer struct{ now func() time.Time }

func newSigner() *signer { return &signer{now: time.Now} }

func (s *signer) Sign(msg string) string { return signWithTime(msg, s.now()) }

// test: s := newSigner(); s.now = func() time.Time { return someFixedTime }
```

### Avoid Embedding Types in Public Structs
Embedding leaks implementation details, inhibits evolution, obscures docs. Embedding adds the
inner type's methods and a field named after the type (public if the type is public); every future
version must keep it. Don't embed `*AbstractList` (or an `AbstractList` interface) in
`ConcreteList`; hold a field and hand-write delegates:

```go
type ConcreteList struct {
  list *AbstractList
}

func (l *ConcreteList) Add(e Entity) { l.list.Add(e) }
func (l *ConcreteList) Remove(e Entity) { l.list.Remove(e) }
```

Breaking changes when embedded: adding methods to an embedded interface, removing methods from an
embedded struct, removing the embedded type, replacing it (even with one satisfying the same
interface). Delegates are tedious but hide details, allow change, and make the full API visible
in docs.

### Avoid Using Built-In Names
Don't use [predeclared identifiers](https://go.dev/ref/spec#Predeclared_identifiers) as names —
they shadow builtins or confuse readers and grep. `var errorMessage string`, not `var error string`;
`func handleErrorMessage(msg string)`, not `(error string)`; fields `err error`, `str string`, not
`error error`, `string string`. The compiler won't complain; `go vet` will.

### Avoid `init()`
Avoid `init()`. If unavoidable, it must:
1. Be fully deterministic regardless of environment/invocation.
2. Not depend on ordering or side effects of other `init()`s.
3. Not touch global/environment state (machine info, env vars, cwd, args).
4. Not do I/O (filesystem, network, syscalls).

Otherwise put it in a helper called from `main()` or in `main()` itself. Libraries must be
deterministic, no "init magic".

```go
var _defaultFoo = defaultFoo() // not: init() { _defaultFoo = Foo{...} }

func loadConfig() Config { /* os.Getwd, os.ReadFile, handle errs */ } // not in init()
```

Acceptable uses: complex expressions not expressible as one assignment; pluggable hooks
(`database/sql` dialects, encoding registries); [Cloud Functions](https://cloud.google.com/functions/docs/bestpractices/tips#use_global_variables_to_reuse_objects_in_future_invocations)
optimizations and other deterministic precomputation.

### Exit in Main
Call `os.Exit`/`log.Fatal*` **only in `main()`**; everything else returns errors. (Never panic to
exit.) Exiting elsewhere gives non-obvious control flow, is hard to test (exits the test binary,
skipping other tests), and skips `defer`red cleanup.

```go
func readFile(path string) (string, error) {
  f, err := os.Open(path)
  if err != nil {
    return "", err
  }
  b, err := io.ReadAll(f)
  if err != nil {
    return "", err
  }
  return string(b), nil
}
```

#### Exit Once
Call `os.Exit`/`log.Fatal` **at most once** in `main()`. Put logic with multiple failure points in
`run()` returning errors — shorter `main`, testable logic, and `defer f.Close()` actually runs.

```go
func main() {
  if err := run(); err != nil {
    log.Fatal(err) // or fmt.Fprintln(os.Stderr, err); os.Exit(1)
  }
}
```

Applies to any library code calling `os.Exit` too. `run()` isn't prescriptive: it may return an
exit code (`os.Exit(run(args))`, testable), take raw args (`run(os.Args[1:])`), take args parsed in
`main`, return a custom error type carrying the exit code, or live outside `package main`. Only
requirement: one place in `main()` exits the process.

### Use Field Tags in Marshaled Structs
Tag every field marshaled to JSON/YAML/etc. The serialized form is a contract; tags make it
explicit and safe against renames.

```go
type Stock struct {
  Price int    `json:"price"`
  Name  string `json:"name"` // safe to rename Name to Symbol
}
```

### Don't Fire-and-Forget Goroutines
Goroutines cost stack memory and scheduling; unmanaged ones in bulk cause perf issues, block GC,
and hold resources. Don't leak them in production; test with
[go.uber.org/goleak](https://pkg.go.dev/go.uber.org/goleak). Every goroutine must have a
predictable stop time or a stop signal, and code must be able to block until it exits.

```go
// Bad: go func() { for { flush(); time.Sleep(delay) } }() — unstoppable
var (
  stop = make(chan struct{}) // tells the goroutine to stop
  done = make(chan struct{}) // tells us that the goroutine exited
)
go func() {
  defer close(done)
  ticker := time.NewTicker(delay)
  defer ticker.Stop()
  for {
    select {
    case <-ticker.C:
      flush()
    case <-stop:
      return
    }
  }
}()

close(stop) // signal
<-done      // wait
```

#### Wait for Goroutines to Exit
- Many goroutines: `var wg sync.WaitGroup`; `wg.Go(...)`; `wg.Wait()`.
- One goroutine: `done := make(chan struct{})`, `defer close(done)` inside, `<-done` to wait.

#### No Goroutines in `init()`
`init()` must not spawn goroutines (user can't control or stop them). A package needing a
background goroutine exposes an object managing its lifetime with a method (`Close`, `Stop`,
`Shutdown`) that signals stop and waits (use `WaitGroup` if it manages several).

```go
func NewWorker(...) *Worker {
  w := &Worker{stop: make(chan struct{}), done: make(chan struct{})}
  go w.doWork() // doWork: defer close(w.done); return on <-w.stop
  return w
}

func (w *Worker) Shutdown() {
  close(w.stop)
  <-w.done
}
```

## Performance
Applies only to the hot path.

- **strconv over fmt** for primitive↔string: `strconv.Itoa(n)` (64 ns, 1 alloc) vs
  `fmt.Sprint(n)` (143 ns, 2 allocs).
- **No repeated string→[]byte conversions:** convert once outside the loop
  (`data := []byte("Hello world")`; 3.25 ns vs 22.2 ns/op).
- **Specify container capacity** to avoid regrowth:
  - Maps: `make(map[string]os.DirEntry, len(files))`. Hint only approximates buckets;
    allocations may still occur up to the hint.
  - Slices: `make([]T, 0, size)` before appending. Capacity is exact: zero allocations until
    len reaches cap (0.21s vs 2.48s in benchmark).

## Style

### Avoid Overly Long Lines
Soft limit **99 chars**; wrap before it, but not a hard limit.

### Be Consistent
Above all, **be consistent**: easier to maintain, reason about, and migrate. Mixed styles cause
overhead, slow reviews, bugs. Apply style changes at package level or larger, not sub-package.

### Group Similar Declarations
Group `import`, `const`, `var`, `type` declarations: `import ("a"; "b")`, not two imports. Only
group related ones — `EnvVar = "MY_ENV"` doesn't belong in the `Operation` enum block. Groups work
inside functions too:

```go
var (
  red   = color.New(0xff0000)
  green = color.New(0x00ff00)
)
```

Exception: adjacent variable declarations inside functions are grouped even if unrelated:

```go
var (
  caller  = c.name
  format  = "json"
  timeout = 5 * time.Second
  err     error
)
```

### Import Group Ordering
Two groups separated by a blank line: standard library, then everything else (goimports default).

### Package Names
All lower-case, no capitals/underscores; no renaming needed at most call sites; short (written at
every call site); not plural (`net/url`, not `net/urls`); not `common`, `util`, `shared`, `lib`.
See [Package Names](https://go.dev/blog/package-names), [Style guideline for Go packages](https://rakyll.org/style-packages/).

### Function Names
MixedCaps. Test functions may use underscores to group cases: `TestMyFunction_WhatIsBeingTested`.

### Import Aliasing
Alias required when the package name ≠ last path element (`client "example.com/client-go"`,
`trace "example.com/trace/v2"`). Otherwise alias only on direct conflict: `"runtime/trace"` plus
`nettrace "golang.net/x/trace"`, not aliasing both.

### Function Grouping and Ordering
Sort functions in rough call order; group by receiver. Order: `struct`/`const`/`var` definitions,
then `newXYZ()`/`NewXYZ()` right after its type, then methods, exported first; plain utility
functions at the end of the file.

### Reduce Nesting
Handle errors/special cases first and return early or `continue`.

```go
for _, v := range data {
  if v.F1 != 1 {
    log.Printf("Invalid v: %v", v)
    continue
  }
  v = process(v)
  if err := v.Call(); err != nil {
    return err
  }
  v.Send()
}
```

### Unnecessary Else
Variable set in both branches → default plus single if: `a := 10; if b { a = 100 }`.

### Top-level Variable Declarations
Use `var`; omit the type unless it differs from the expression's: `var _s = F()`, but
`var _e error = F()` when `F` returns `myError` and you want `error`.

### Prefix Unexported Globals with _
Prefix unexported top-level `var`s and `const`s with `_` (`_defaultPort`) so global use is
obvious; generic names risk using the wrong value from another file, and a local `defaultPort :=`
shadowing it compiles silently. Exception: unexported errors use `err` prefix (see Error Naming).

### Embedding in Structs
Embedded types go at the top of the field list, separated from regular fields by a blank line.
Embedding must give tangible, semantically appropriate benefit with zero adverse user-facing
effects. Never embed mutexes.

Embedding **must not**: be purely cosmetic/convenience; make the outer type harder to construct
or use; break a useful zero value (embedding `io.ReadWriter` → nil-pointer panics; `bytes.Buffer`
is fine); expose unrelated functions/fields or unexported types; change copy semantics; change the
outer API or type semantics; embed a non-canonical form of the inner type; expose implementation
details or let users observe/control internals; change inner behavior via wrapping in surprising
ways.

Litmus test: would *all* inner exported methods/fields be added directly to the outer type? If
"some" or "no", use a named field.

```go
type countingWriteCloser struct {
  io.WriteCloser // Good: Write is overridden with a purpose and delegates

  count int
}

type Client struct { // not embedding Mutex, WaitGroup, Buffer, URL
  mtx sync.Mutex
  wg  sync.WaitGroup
  buf bytes.Buffer
  url url.URL
}
```

### Local Variable Declarations
Use `:=` when setting an explicit value (`s := "foo"`). Use `var` when the zero value is clearer,
e.g. [empty slices](https://go.dev/wiki/CodeReviewComments#declaring-empty-slices): `var filtered []int`, not
`filtered := []int{}`.

### nil is a Valid Slice
- Return `nil`, not `[]int{}`.
- Check emptiness with `len(s) == 0`, never `s == nil`.
- `var nums []int` is usable with `append` without `make()`.

A nil slice is not identical to an allocated empty slice; they may differ (e.g. serialization).

### Reduce Scope of Variables
Narrow scope of vars and consts where it doesn't conflict with Reduce Nesting:
`if err := os.WriteFile(name, data, 0644); err != nil { return err }`. If the result is needed
outside the `if`, don't scope it in (no `if data, err := ...; err == nil {...} else {...}`).
Constants needn't be global unless used in multiple functions/files or part of the package's
external contract — declare them inside the function otherwise.

### Avoid Naked Parameters
Add `/* name */` comments when meaning isn't obvious: `printInfo("foo", true /* isLocal */, true
/* done */)`. Better: replace naked `bool`s with custom types (allows more than two states later):

```go
type Region int

const (
  UnknownRegion Region = iota
  Local
)

func printInfo(name string, region Region, status Status)
```

### Use Raw String Literals to Avoid Escaping
`` wantError := `unknown error:"test"` `` not `"unknown name:\"test\""`.

### Initializing Structs
- **Field names:** always (`go vet` enforces): `User{FirstName: "John", LastName: "Doe", Admin:
  true}`, not `User{"John", "Doe", true}`. Exception: test tables with ≤3 fields may omit them.
- **Omit zero-value fields** unless the name adds meaning (e.g. `{give: "0", want: 0}` in test
  tables). Don't write `MiddleName: "", Admin: false`.
- **All fields omitted** → `var user User`, not `user := User{}` (matches map/slice conventions).
- **References:** `&T{Name: "bar"}`, not `new(T)` then assign.

### Initializing Maps
Use `make(...)` for empty and programmatically populated maps (visually distinct from a nil
`var m map[T1]T2` declaration, easy to add size hints; give hints when possible). Use a map literal
for a fixed set of elements known at init:

```go
m := map[T1]T2{
  k1: v1,
  k2: v2,
}
```

### Format Strings outside Printf
Format strings declared outside the call must be `const` so `go vet` can check them:
`const msg = "unexpected values %v, %v\n"`.

### Naming Printf-style Functions
Use predefined [Printf family](https://pkg.go.dev/cmd/vet#hdr-Printf_family) names so `go vet`
checks them. Otherwise end the name with `f` (`Wrapf`, not `Wrap`) and check with
`go vet -printfuncs=wrapf,statusf`.

## Patterns

### Test Tables
Use table-driven tests with [subtests](https://go.dev/blog/subtests) when testing multiple
conditions where inputs/outputs vary — less duplication, easier context and new cases. Slice is
`tests`, each case `tt`; prefix fields with `give`/`want`.

```go
tests := []struct {
  give     string
  wantHost string
  wantPort string
}{
  {give: "192.0.2.0:8000", wantHost: "192.0.2.0", wantPort: "8000"},
  {give: ":8000", wantHost: "", wantPort: "8000"},
}

for _, tt := range tests {
  t.Run(tt.give, func(t *testing.T) {
    host, port, err := net.SplitHostPort(tt.give)
    require.NoError(t, err)
    assert.Equal(t, tt.wantHost, host)
    assert.Equal(t, tt.wantPort, port)
  })
}
```

#### Avoid Unnecessary Complexity in Table Tests
Don't use tables when subtests need complex or conditional logic inside the loop; split into
multiple tables or separate `Test...` functions. Aim for:
- narrowest unit of behavior;
- minimal "test depth" (number of successive assertions depending on earlier ones), no
  conditional assertions;
- every table field used and all test logic run in every case.

Red flags: multiple branching fields (`shouldError`, `expectCall`), many `if`s for mock
expectations (`shouldCallFoo`), functions in the table (`setupMocks func(*FooMock)`). Instead
write e.g. `TestShouldCallX` and `TestShouldCallYAndFail` with straight-line mock setup.
Grouping cases that differ only by input is fine and aids comparison. A single success/failure
branch via `shouldErr` is OK if the body is short. When unsure, favor readability.

#### Parallel Tests
Parallel tests and loops that spawn goroutines or capture references must have a loop variable
scoped to the iteration (`tt` per iteration with `t.Parallel()`), or cases see wrong/changing
values.

### Functional Options
For optional args in constructors and public APIs that may grow, especially with ≥3 args: accept
`opts ...Option` instead of required params (`db.Open(addr)`, `db.Open(addr,
db.WithLogger(log))` vs always passing `db.DefaultCache, zap.NewNop()`).

Implement with an `Option` interface holding an unexported method that records onto an
unexported `options` struct:

```go
type options struct {
  cache  bool
  logger *zap.Logger
}

type Option interface {
  apply(*options)
}

type cacheOption bool

func (c cacheOption) apply(opts *options) { opts.cache = bool(c) }

func WithCache(c bool) Option { return cacheOption(c) }

type loggerOption struct{ Log *zap.Logger }

func (l loggerOption) apply(opts *options) { opts.logger = l.Log }

func WithLogger(log *zap.Logger) Option { return loggerOption{Log: log} }

func Open(addr string, opts ...Option) (*Connection, error) {
  options := options{cache: defaultCache, logger: zap.NewNop()}
  for _, o := range opts {
    o.apply(&options)
  }
  // ...
}
```

Prefer this over closures: options are comparable in tests/mocks and can implement other
interfaces such as `fmt.Stringer`. See [Self-referential functions](https://commandcenter.blogspot.com/2014/01/self-referential-functions-and-design.html),
[Functional options for friendly APIs](https://dave.cheney.net/2014/10/17/functional-options-for-friendly-apis).

## Linting
Lint consistently across the codebase. Minimum set: `errcheck` (errors handled), `goimports`
(format, imports), `revive` (style; successor of deprecated `golint`), `govet`, `staticcheck`.
Run them via [golangci-lint](https://github.com/golangci/golangci-lint); add more linters as the
project needs.
