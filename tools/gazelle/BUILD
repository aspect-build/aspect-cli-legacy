load("@aspect_gazelle_prebuilt//:def.bzl", "aspect_gazelle")

package(default_visibility = ["//visibility:public"])

aspect_gazelle(
    name = "gazelle",
    languages = [
        "proto",
        "go",
    ],
    repo_config = "@bazel_gazelle_go_repository_config//:WORKSPACE",
)
