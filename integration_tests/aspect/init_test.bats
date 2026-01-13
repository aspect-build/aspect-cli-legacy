load "common.bats"

setup() {
    cd "$TEST_REPO" || exit 1
}

# fix: re-enable test once fixed
# @test 'aspect init should create functional workspace' {
#     cd "$TEST_TMPDIR"
#     aspect init --preset=minimal
#     cd scaffold_test*
#     aspect build //...
# }
