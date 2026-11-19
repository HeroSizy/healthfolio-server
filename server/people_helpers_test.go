package server

// The tests written before people call the tools on the owner's data.

func (m *MCP) owner() *mcpDB {
	p := Person{Num: ownerNum, ID: ownerKey, Owner: true}
	d, _ := m.open(p)
	return d
}

func (m *MCP) call(name string, a map[string]any) (any, error) { return m.owner().call(name, a) }

func (m *MCP) rows(q string, args ...any) ([]map[string]any, error) {
	return m.owner().rows(q, args...)
}
