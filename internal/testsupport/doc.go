// Package testsupport provides in-memory fakes for application and handler unit tests.
//
// Fakes here do not replace PostgreSQL integration tests under internal/*/infrastructure
// (*_pg_test.go or //go:build integration). Repository SQL, JSONB round-trips, and locking
// must run via just verify-repo with dbtest.Open.
//
// testsupport 为 application / handler 单测提供内存 Fake；不替代 infrastructure 真库集成测。
package testsupport
