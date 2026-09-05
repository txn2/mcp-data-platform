// Package mcpdataplatform names the module root. It declares no API and is
// imported by nothing: the code lives under cmd/, pkg/ and internal/, and the
// structural gates that walk the tree live in test/structure/.
//
// It exists because `swag init --parseDependency` (make swagger) runs `go list`
// in the directory it is pointed at, and a root with no Go files at all fails
// that lookup -- after which swag cannot resolve a type from an imported
// package and the generated OpenAPI document silently loses it. Until the gates
// moved out of the root, their test files were what kept `go list .` answering.
package mcpdataplatform
