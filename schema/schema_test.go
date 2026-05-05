package schema

import "testing"

func TestParseCreateTable(t *testing.T) {
	sql := `
CREATE TABLE users (
    id bigint PRIMARY KEY,
    email varchar(255) NOT NULL,
    created_at timestamptz DEFAULT now()
);

CREATE TABLE orders (
    id bigserial PRIMARY KEY,
    user_id bigint REFERENCES users(id),
    total numeric(10, 2)
);
`
	s, err := Parse(sql)
	if err != nil {
		t.Fatal(err)
	}

	if got := len(s.Tables); got != 2 {
		t.Fatalf("want 2 tables, got %d", got)
	}

	users, ok := s.Tables["users"]
	if !ok {
		t.Fatal("users table missing")
	}
	wantUsers := []Column{
		{Name: "id", Type: "int8"},
		{Name: "email", Type: "varchar"},
		{Name: "created_at", Type: "timestamptz"},
	}
	if len(users.Columns) != len(wantUsers) {
		t.Fatalf("users: want %d cols, got %d", len(wantUsers), len(users.Columns))
	}
	for i, w := range wantUsers {
		if users.Columns[i].Name != w.Name || users.Columns[i].Type != w.Type {
			t.Errorf("users.Columns[%d] = %+v, want %+v", i, *users.Columns[i], w)
		}
	}

	orders, ok := s.Tables["orders"]
	if !ok {
		t.Fatal("orders table missing")
	}
	if len(orders.Columns) != 3 {
		t.Errorf("orders: want 3 cols, got %d", len(orders.Columns))
	}
}

func TestParseIgnoresNonCreateTable(t *testing.T) {
	sql := `
CREATE INDEX idx_users_email ON users(email);
SELECT 1;
`
	s, err := Parse(sql)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Tables) != 0 {
		t.Errorf("want 0 tables, got %d", len(s.Tables))
	}
}

func TestParsePositions(t *testing.T) {
	sql := "CREATE TABLE users (\n    id bigint,\n    email text\n);\n"
	// 0-indexed column offsets in each line:
	//   line 0: "CREATE TABLE users (" — "users" at char 13
	//   line 1: "    id bigint,"      — "id"    at char 4
	//   line 2: "    email text"      — "email" at char 4
	s, err := Parse(sql)
	if err != nil {
		t.Fatal(err)
	}
	tbl := s.Tables["users"]
	if tbl == nil {
		t.Fatal("users table missing")
	}
	if got := tbl.Position; got.Line != 0 || got.Character != 13 {
		t.Errorf("table position = %+v, want {Line:0 Character:13}", got)
	}
	want := []struct {
		name string
		line int
		char int
	}{
		{"id", 1, 4},
		{"email", 2, 4},
	}
	for i, w := range want {
		c := tbl.Columns[i]
		if c.Name != w.name || c.Position.Line != w.line || c.Position.Character != w.char {
			t.Errorf("col %d = %s @ %d:%d, want %s @ %d:%d",
				i, c.Name, c.Position.Line, c.Position.Character, w.name, w.line, w.char)
		}
	}
}

func TestParseError(t *testing.T) {
	_, err := Parse("CREATE TABLE (")
	if err == nil {
		t.Fatal("want error, got nil")
	}
}
