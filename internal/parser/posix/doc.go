// Package posix parses POSIX/bash/zsh command lines by walking the
// mvdan.cc/sh AST with a strict node whitelist; anything else becomes an
// unsupported construct (PROTOTYPE_SPEC.md §14.2, §14.3).
package posix
