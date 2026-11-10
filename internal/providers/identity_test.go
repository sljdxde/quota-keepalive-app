package providers

import "testing"

func TestPublicIdentityKeepsReadableName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"user@example.com", "user@example.com"},
		{"13800138000", "13800138000"},
		{"iam::49b62bcca42a4fa3a17a928770b8e91d:user:hw083749283", "hw083749283"},
		{"iam::01a187e8f2517c09aae539409bff9f30:user:hw063442711284743", "hw063442711284743"},
		{"", ""},
		{"a.b.c.d.e.f.g.h.i.j.k.l.m.n.o.p.q.r.s.t.u.v.w.x.y.z.very.long.jwt.like.value", ""},
	}
	for _, tc := range cases {
		if got := publicIdentity(tc.in); got != tc.want {
			t.Fatalf("publicIdentity(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
