load "common.bats"

setup() {
    cd "$TEST_REPO" || exit 1
    mkdir -p test
    echo "exit 0" >test/target.sh
    chmod +x test/target.sh
    echo 'sh_binary(name = "target", srcs = ["target.sh"])' >test/BUILD.bazel
    export ASPECT_OTEL_OUT="${TEST_TMPDIR}/otel.json"
}

teardown() {
    unset ASPECT_OTEL_OUT
    rm -rf test
    rm -f "${TEST_TMPDIR}/otel.json"
}

@test 'ASPECT_OTEL_OUT causes spans to be written on exit' {
    run aspect run //test:target
    assert_success
    [ -s "${TEST_TMPDIR}/otel.json" ]
}

@test 'telemetry output contains expected resource attributes' {
    run aspect run //test:target
    assert_success
    grep -q '"service.name"' "${TEST_TMPDIR}/otel.json"
    grep -q '"Aspect CLI"' "${TEST_TMPDIR}/otel.json"
    grep -q '"service.version"' "${TEST_TMPDIR}/otel.json"
    grep -q '"host.name"' "${TEST_TMPDIR}/otel.json"
    grep -q '"os.type"' "${TEST_TMPDIR}/otel.json"
    grep -q '"process.pid"' "${TEST_TMPDIR}/otel.json"
    grep -q '"process.executable.name"' "${TEST_TMPDIR}/otel.json"
    grep -q '"process.owner"' "${TEST_TMPDIR}/otel.json"
    grep -q '"process.runtime.version"' "${TEST_TMPDIR}/otel.json"
    grep -q '"process.working_directory"' "${TEST_TMPDIR}/otel.json"
    grep -q '"telemetry.sdk.name"' "${TEST_TMPDIR}/otel.json"
}

@test 'bazel.command and bazel.args span attributes are set' {
    run aspect run --keep_going //test:target -- --these //please:pkg
    assert_success
    grep -q '"bazel.command"' "${TEST_TMPDIR}/otel.json"
    grep -q '"bazel.args"' "${TEST_TMPDIR}/otel.json"
    grep -A 15 '"bazel\.args"' "${TEST_TMPDIR}/otel.json" | grep -q '"--keep_going"'
    grep -A 15 '"bazel\.args"' "${TEST_TMPDIR}/otel.json" | grep -q '"//please:pkg"'
}

@test 'bazel.targets includes targets and excludes flags' {
    run aspect run --keep_going //test:target -- //not:these
    assert_success
    run grep -o '"bazel\.targets","Value":{"Type":"STRINGSLICE","Value":\[[^]]*\]' "${TEST_TMPDIR}/otel.json"
    assert_output --partial '"//test:target"'
    refute_output --partial '"--keep_going"'
    refute_output --partial '"//not:these"'
}
