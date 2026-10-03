package daemon_test

// R55-F31 T3: TestAuditClassification. Every audit action belongs to one of the
// four classes of Docs/protocol/audit.md §When the row cannot be written (S, S-,
// L, N), and every call site writes it the way its class says. The scan is a
// go/parser AST walk, not a regex: it resolves the action argument (a string
// literal or a package constant), follows a fixed list of wrappers, and sees
// `if err := log.Append(...); err != nil { return err }`.
//
// It proves the call kind, not that the transaction is the change's own; the
// fault-injection tests (T4, T5) prove that.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Call kinds a site can have.
const (
	kAppend = "Append"       // Log.Append: own transaction, after the commit
	kTx     = "AppendTx"     // in the change's transaction, failure aborts it
	kSoft   = "AppendTxSoft" // in the change's transaction, failure only logged
)

type auditClass struct {
	name  string
	kinds []string // the call kinds the class allows
}

var (
	classS  = auditClass{"S", []string{kTx}}
	classSm = auditClass{"S-", []string{kSoft}}
	classL  = auditClass{"L", []string{kAppend, kTx, kSoft}}
	classN  = auditClass{"N", []string{kAppend, kSoft}}
)

// actionClass is the class table. A few actions have a second legitimate call
// kind, named in actionExtra with the reason.
var actionClass = map[string]auditClass{}

func init() {
	for _, a := range []string{
		"approval.create", "approval.approve", "grant.create", "grant.auto", "grant.issue", "grant.policy_add",
		"ws.release", "peer.verify", "pair.complete", "team.roster_apply", "team.invite_issued",
		"device.link_intent", "device.link_active", "device.scope_set", "debate.constraint", "data.prune",
		"decision.create", "decision.sign_in",
	} {
		actionClass[a] = classS
	}
	for _, a := range []string{
		"approval.reject", "approval.bad_code", "approval.locked", "grant.revoke", "grant.revoked_in",
		"grant.policy_remove", "peer.remove", "device.unlink", "device.scope_clear", "decision.refuse",
	} {
		actionClass[a] = classSm
	}
	for _, a := range []string{
		"daemon.start", "daemon.stop", "daemon.stop_requested", "approval.mode", "identity.create",
		"service.install", "service.uninstall", "audit.chain_start",
	} {
		actionClass[a] = classL
	}
	for _, a := range nActions {
		actionClass[a] = classN
	}
}

// actionExtraFile is the one file where each actionExtra kind is allowed, so a
// new site elsewhere cannot use it (review 97 I7).
var actionExtraFile = map[string]string{
	"grant.create":       "internal/daemon/grant.go",
	"team.invite_issued": "internal/daemon/team.go",
	"peer.remove":        "internal/daemon/trust.go",
}

// actionExtra lists the one-off second call kinds.
var actionExtra = map[string][]string{
	// grant.create is S on the policy path (AppendTx) and N on the approval
	// path (a pending row grants nothing; grant.issue is the S row).
	"grant.create": {kAppend},
	// team.invite_issued is S without a transaction: Append, then the pairing
	// is cancelled if the row fails (audit.md).
	"team.invite_issued": {kAppend},
	// peer.remove is S- in the removal's transaction; when the team GC already
	// removed the row there is no transaction and it is an N row.
	"peer.remove": {kAppend},
}

// nActions is every other action the daemon writes (class N).
var nActions = []string{
	"approval.limit", "approval.open",
	"debate.abandon", "debate.close", "debate.entry", "debate.entry_in", "debate.ignored", "debate.reveal",
	"debate.reveal_bad", "debate.reveal_in", "debate.start", "debate.close_in", "debate.constraint_in",
	"request.orphan", "request.cancel_refused", "request.conflict", "request.duplicate", "request.auto_decline",
	"ws.orphan",
	"device.out_of_scope", "device.run",
	"experience.write",
	"grant.conflict", "grant.fetch", "grant.fetch_summary", "grant.in", "grant.orphan", "grant.refused",
	"mail.expired", "mail.in", "mail.reject", "mailbox.rotate",
	"notify.config", "notify.fail",
	"pair.attempt_fail", "pair.fail", "pair.start", "peer.verify_fail", "peers.list_skip",
	"presence.human", "presence.mode",
	"relay.reject_summary",
	"request.accept", "request.cancel", "request.cancel_in", "request.complete", "request.decline",
	"request.defer", "request.in", "request.resend", "request.state", "request.submit",
	"session.open",
	"team.create", "team.delete", "team.invite", "team.join", "team.join_ignored", "team.leave",
	"team.leave_ignored", "team.member_add", "team.member_leave", "team.member_remove", "team.rename",
	"team.roster_ignored",
	"ws.open", "ws.accept_result", "ws.cancel", "ws.cancel_in", "ws.close", "ws.discard", "ws.request_changes",
	"ws.result", "ws.result_in", "ws.result_mismatch", "ws.state", "ws.early_complete", "ws.ignored",
}

// wrapper describes a function that forwards to an append call. action is the
// index of the action argument, or -1 with fixed naming the action it always
// writes. kind is the kind the wrapper writes with.
type wrapper struct {
	action int
	fixed  string
	kind   string
}

var wrappers = map[string]wrapper{
	"m.audit":           {2, "", kAppend}, // peers.Manager.audit(ctx, actor, action, detail)
	"s.audit":           {1, "", kAppend}, // debate.Store.audit(actor, action, detail) returns an after-commit func
	"r.audit":           {1, "", kAppend}, // device runner: r.audit(ctx, action, detail)
	"s.audited":         {2, "", kAppend}, // team.Store.audited(ctx, actor, action, detail)
	"s.auditLifecycle":  {2, "", kAppend}, // request.Store.auditLifecycle(ctx, actor, action, ...)
	"s.createAudit":     {-1, "decision.create", kTx},
	"s.refuseAudit":     {-1, "decision.refuse", kSoft},
	"s.auditOpen":       {-1, "ws.open", kSoft},
	"s.auditRemovedTx":  {-1, "peer.remove", kSoft},
	"auditGrantRevokes": {-1, "grant.revoke", kSoft},
	"auditTx":           {3, "", kTx}, // daemon.auditTx(ctx, tx, actor, action, detail)
}

// wrapperDefs are the functions whose own bodies forward a variable action,
// keyed by "<directory>:<function>" so a function elsewhere that merely shares
// a name is not exempted (review 97 I7). The fixed-action wrappers
// (createAudit, refuseAudit, auditOpen, auditRemovedTx, auditGrantRevokes)
// are not listed: their bodies are checked like any other call site.
var wrapperDefs = map[string]bool{
	"internal/audit:Append": true, "internal/audit:AppendTx": true, "internal/audit:AppendTxSoft": true,
	"internal/daemon:auditTx": true, "internal/daemon:audit": true, // helperRunner.audit (device_run.go)
	"internal/debate:audit": true, "internal/peers:audit": true, "internal/team:audited": true,
	"internal/request:auditLifecycle": true, "internal/capability:record": true,
}

// noErrWrappers return no audit error (they return an after-commit func or
// nothing), so `return s.audit(...)` is not an error return.
var noErrWrappers = map[string]bool{"s.audit": true, "r.audit": true, "s.audited": true, "s.auditLifecycle": true, "m.audit": true}

// variableAction lists the files whose append calls take the action from a
// variable by design, with the class every action written there has.
var variableAction = map[string]string{
	"internal/daemon/fetch.go": "N: grant.fetch and grant.fetch_summary, rate-limited rows (OD-F31-2)",
	"cmd/agentnetd/install.go": "L: service.install and service.uninstall",
}

type auditSite struct {
	file   string
	line   int
	action string // "" when not constant
	kind   string
	ret    bool // the call's error is returned to the caller
	inDef  bool // inside a wrapper's own body
	via    string
}

func TestAuditClassification(t *testing.T) {
	// consts is keyed by "<package>.<name>".
	consts := map[string]string{}
	conflict := map[string]bool{}
	type parsed struct {
		path string
		pkg  string
		f    *ast.File
	}
	var files []parsed
	fset := token.NewFileSet()
	for _, root := range []string{"../../internal", "../../cmd"} {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") ||
				strings.Contains(filepath.ToSlash(p), "/testdata/") {
				return nil
			}
			f, err := parser.ParseFile(fset, p, nil, 0)
			if err != nil {
				return err
			}
			files = append(files, parsed{filepath.ToSlash(p), f.Name.Name, f})
			for _, decl := range f.Decls {
				gd, ok := decl.(*ast.GenDecl)
				if !ok || gd.Tok != token.CONST {
					continue
				}
				for _, sp := range gd.Specs {
					vs := sp.(*ast.ValueSpec)
					for i, n := range vs.Names {
						if i >= len(vs.Values) {
							continue
						}
						lit, ok := vs.Values[i].(*ast.BasicLit)
						if !ok || lit.Kind != token.STRING {
							continue
						}
						v, _ := strconv.Unquote(lit.Value)
						key := f.Name.Name + "." + n.Name
						if old, seen := consts[key]; seen && old != v {
							conflict[key] = true
						}
						consts[key] = v
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(files) < 100 {
		t.Fatalf("scanned only %d files", len(files))
	}

	resolve := func(pkg string, e ast.Expr) (string, bool) {
		switch v := e.(type) {
		case *ast.BasicLit:
			if v.Kind == token.STRING {
				s, _ := strconv.Unquote(v.Value)
				return s, true
			}
		case *ast.Ident:
			if s, ok := consts[pkg+"."+v.Name]; ok && !conflict[pkg+"."+v.Name] {
				return s, true
			}
		case *ast.SelectorExpr:
			if x, ok := v.X.(*ast.Ident); ok {
				key := x.Name + "." + v.Sel.Name
				if s, ok := consts[key]; ok && !conflict[key] {
					return s, true
				}
			}
		}
		return "", false
	}
	callName := func(c *ast.CallExpr) string {
		switch f := c.Fun.(type) {
		case *ast.Ident:
			return f.Name
		case *ast.SelectorExpr:
			if x, ok := f.X.(*ast.Ident); ok {
				return x.Name + "." + f.Sel.Name
			}
			return "?." + f.Sel.Name
		}
		return ""
	}

	var sites []auditSite
	for _, pf := range files {
		var stack []ast.Node
		dir := strings.TrimPrefix(filepath.ToSlash(filepath.Dir(pf.path)), "../../")
		inDef := func() bool {
			for _, n := range stack {
				if fd, ok := n.(*ast.FuncDecl); ok && wrapperDefs[dir+":"+fd.Name.Name] {
					return true
				}
			}
			return false
		}
		// retCalls holds calls whose error is returned: `return f(...)` and the
		// init call of an `if` whose body returns the error.
		retCalls := map[*ast.CallExpr]bool{}
		ast.Inspect(pf.f, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.ReturnStmt:
				for _, r := range v.Results {
					if c, ok := r.(*ast.CallExpr); ok {
						retCalls[c] = true
					}
				}
			case *ast.IfStmt:
				as, ok := v.Init.(*ast.AssignStmt)
				if !ok || len(as.Rhs) != 1 || len(as.Lhs) != 1 {
					return true
				}
				c, ok := as.Rhs[0].(*ast.CallExpr)
				id, ok2 := as.Lhs[0].(*ast.Ident)
				if !ok || !ok2 {
					return true
				}
				returned := false
				ast.Inspect(v.Body, func(m ast.Node) bool {
					if r, ok := m.(*ast.ReturnStmt); ok {
						for _, res := range r.Results {
							ast.Inspect(res, func(x ast.Node) bool {
								if i, ok := x.(*ast.Ident); ok && i.Name == id.Name {
									returned = true
								}
								return true
							})
						}
					}
					return true
				})
				if returned {
					retCalls[c] = true
				}
			}
			return true
		})
		ast.Inspect(pf.f, func(n ast.Node) bool {
			if n == nil {
				stack = stack[:len(stack)-1]
				return true
			}
			stack = append(stack, n)
			c, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			pos := fset.Position(c.Pos())
			name := callName(c)
			site := auditSite{file: pf.path, line: pos.Line, ret: retCalls[c], inDef: inDef(), via: name}
			sel, isSel := c.Fun.(*ast.SelectorExpr)
			switch {
			case isSel && sel.Sel.Name == "Append" && len(c.Args) == 4:
				site.kind = kAppend
				site.action, _ = resolve(pf.pkg, c.Args[2])
			case isSel && sel.Sel.Name == "AppendTx" && len(c.Args) == 5:
				site.kind = kTx
				site.action, _ = resolve(pf.pkg, c.Args[3])
			case isSel && sel.Sel.Name == "AppendTxSoft" && len(c.Args) == 5:
				site.kind = kSoft
				site.action, _ = resolve(pf.pkg, c.Args[3])
			default:
				w, ok := wrappers[name]
				if !ok {
					return true
				}
				site.kind = w.kind
				if w.action < 0 {
					site.action = w.fixed
				} else if w.action < len(c.Args) {
					site.action, _ = resolve(pf.pkg, c.Args[w.action])
				}
			}
			sites = append(sites, site)
			return true
		})
	}

	// Calls to Append with four arguments that are not audit appends (a journal,
	// a buffer) have a non-audit action: they show up as unresolved and are
	// listed here with the reason.
	notAudit := map[string]bool{
		"internal/relay": true, // the relay's own journal, not audit_events
	}
	seen := map[string]bool{}
	var bad []string
	for _, s := range sites {
		dir := filepath.ToSlash(filepath.Dir(s.file))
		if notAudit[strings.TrimPrefix(dir, "../../")] {
			continue
		}
		loc := strings.TrimPrefix(s.file, "../../") + ":" + strconv.Itoa(s.line)
		if s.action == "" {
			if _, ok := variableAction[strings.TrimPrefix(s.file, "../../")]; !ok && !s.inDef {
				bad = append(bad, loc+": the action is not constant and the call is not inside a listed wrapper ("+s.via+")")
			}
			continue
		}
		seen[s.action] = true
		cl, ok := actionClass[s.action]
		if !ok {
			bad = append(bad, loc+": action "+s.action+" is in no class")
			continue
		}
		kinds := append([]string{}, cl.kinds...)
		if strings.TrimPrefix(s.file, "../../") == actionExtraFile[s.action] {
			kinds = append(kinds, actionExtra[s.action]...)
		}
		allowed := false
		for _, k := range kinds {
			if k == s.kind {
				allowed = true
			}
		}
		if !allowed && !s.inDef {
			bad = append(bad, loc+": "+cl.name+" action "+s.action+" is written with "+s.kind+" (allowed: "+strings.Join(kinds, ", ")+")")
		}
		if cl.name == "N" && s.ret && !s.inDef && s.kind == kAppend && !noErrWrappers[s.via] {
			bad = append(bad, loc+": N action "+s.action+" returns its audit error to the caller")
		}
	}
	sort.Strings(bad)
	for _, b := range bad {
		t.Error(b)
	}
	// A class entry nobody writes any more is a stale table row.
	for a := range actionClass {
		if !seen[a] {
			t.Logf("note: class table lists %s, no call site writes it as a constant", a)
		}
	}
}
