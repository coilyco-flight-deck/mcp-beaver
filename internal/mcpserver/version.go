package mcpserver

// Version is the release this binary was built from, stamped by the release
// build with -X. It is "dev" in every other build, which is the honest answer
// for a binary that no release produced.
//
// One stamp target for the whole runtime: the `version` command prints it and
// every MCP handshake advertises it, so a host reading serverInfo and an
// operator reading the CLI never disagree about which build is running.
var Version = "dev"
