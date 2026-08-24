// Package scope normalizes and canonicalizes paths and classifies them into
// scopes (SYSTEM, WORKSPACE, WORKSPACE_GENERATED, HOME, OUTSIDE_WORKSPACE)
// with sensitivity, tool-cache and escape flags (PROTOTYPE_SPEC.md §16).
package scope
