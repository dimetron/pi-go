package cli

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/provider"
)

// roleFlags lists the roles pi can run as, in the order the footer prints them,
// paired with the flag that selects each one.
var roleFlags = []struct{ role, flag string }{
	{"default", ""},
	{"smol", "--smol"},
	{"slow", "--slow"},
	{"plan", "--plan"},
}

// writeRoleSummary appends the resolved role table to `pi --help`.
//
// The static help can only describe how a model name maps to a provider; this
// answers the question actually being asked, which is what --smol and --slow
// will do on *this* machine. It reads the live config, so it also surfaces a
// role that silently falls back to default and a provider whose credential is
// not in the environment — two things that otherwise only show up as a failure
// at the first request.
//
// Help output must never fail. Every error path here degrades to a shorter
// table or a single hint line rather than returning one.
func writeRoleSummary(w io.Writer) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(w, "\nConfigured roles: unavailable (%v)\n", err)
		return
	}

	keys := config.APIKeys()

	// Both paths are named because config.Load merges them and a role shown
	// here may come from either; pointing at only the global file would send
	// someone editing the wrong one.
	fmt.Fprint(w, "\nConfigured roles (~/.pi-go/config.json, overridden by ./.pi-go/config.json):\n\n")

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  ROLE\tFLAG\tMODEL\tPROVIDER\tCREDENTIAL")

	for _, rf := range roleFlags {
		flag := rf.flag
		if flag == "" {
			flag = "-"
		}

		rc, configured := cfg.Roles[rf.role]
		if !configured || rc.Model == "" {
			// Not an error: ResolveRole falls back to default, so the role
			// still works. Saying so beats printing the default's model twice
			// and leaving the reader to infer it.
			fmt.Fprintf(tw, "  %s\t%s\t%s\n", rf.role, flag, "(falls back to default)")
			continue
		}

		model, prov, _, _, _, resolveErr := cfg.ResolveRole(rf.role)
		if resolveErr != nil {
			fmt.Fprintf(tw, "  %s\t%s\t%s\n", rf.role, flag, "("+resolveErr.Error()+")")
			continue
		}

		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n", rf.role, flag, model, prov, credentialStatus(prov, model, keys))
	}

	_ = tw.Flush()

	fmt.Fprint(w, "\n  Change these in config.json under \"roles\", or override per run with --model.\n")
}

// credentialStatus reports which environment variable authenticates prov, and
// whether it is currently set.
//
// A local Ollama daemon is the one provider that needs no credential, so
// reporting a missing OLLAMA_API_KEY there would be a false alarm — the key is
// only required once a :cloud tag routes the request to api.ollama.com.
//
// A cloud tag with no key is not a missing credential either: the request falls
// back to the local daemon, which proxies cloud models on the signed-in
// identity. Calling that MISSING would report a failure that does not happen.
//
// agentgateway is a local OpenAI-compatible gateway and needs no credential
// either; AGENTGATEWAY_API_KEY is only for a gateway that requires one.
func credentialStatus(prov, model string, keys map[string]string) string {
	if prov == "ollama" && !provider.IsOllamaCloudModel(model) {
		return "none (local daemon)"
	}
	if prov == "agentgateway" {
		return "none (local gateway)"
	}

	envVar := providerEnvVar(prov)
	if _, ok := keys[prov]; ok {
		return envVar + " (set)"
	}
	if prov == "ollama" {
		return envVar + " (unset — using local daemon)"
	}
	return envVar + " (MISSING)"
}
