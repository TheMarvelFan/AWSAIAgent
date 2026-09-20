// Package terraform implements runner.Runner by shelling out to the Terraform
// CLI against pre-written modules.
//
// The critical property, and the reason this package is shaped the way it is:
// NO INFRASTRUCTURE CODE IS GENERATED FROM MODEL OUTPUT. The generator emits a
// root module consisting of a provider block and one `module` block per
// catalog block. Each module block names a directory that ships with the
// binary and passes validated variables. The model chose a template id and
// some parameters; it never wrote a line of HCL, and there is no path by which
// it could.
package terraform

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/cloud-ai/ai-aws-architect/internal/runner"
)

// architecture is the subset of the config document this package reads.
type architecture struct {
	Region string `json:"region"`
	Blocks []struct {
		ID         string         `json:"id"`
		TemplateID string         `json:"template_id"`
		Parameters map[string]any `json:"parameters"`
	} `json:"blocks"`
}

// generateRoot writes the root module HCL for a request.
//
// Everything interpolated into the output is either a fixed string, a value
// from the deployment record, or a parameter that catalog.Validate has already
// checked against a type, an enum, a numeric range or a regex. Nothing arrives
// here unvalidated.
func generateRoot(req runner.Request, arch architecture, availableModules map[string]bool) (string, error) {
	var sb strings.Builder

	sb.WriteString(`terraform {
  required_version = ">= 1.10"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }

    # Used by the serverless module to build its deployment package at plan
    # time. Tiny compared with the AWS provider, and it keeps the handler in
    # the module rather than requiring a checked-in binary artefact.
    archive = {
      source  = "hashicorp/archive"
      version = "~> 2.0"
    }
  }

  # Partial configuration: the bucket, key and region are supplied by
  # -backend-config flags at init time, because they come from server config
  # rather than from anything in this file.
  backend "s3" {}
}

`)

	// The provider assumes the customer role itself rather than receiving
	// credentials in its environment. A subprocess gets its environment once at
	// launch, so injected credentials would expire mid-run on anything longer
	// than the session; the provider refreshes internally.
	_, _ = fmt.Fprintf(&sb, `provider "aws" {
  region = %q

  assume_role {
    role_arn     = %q
    external_id  = %q
    session_name = %q
  }

  default_tags {
    tags = {
`, arch.Region, req.Target.RoleARN, req.Target.ExternalID, sessionName(req))

	// Sorted so identical inputs produce byte-identical HCL. That matters:
	// a plan hash is only meaningful if the thing being hashed is stable.
	for _, k := range sortedKeys(req.Tags) {
		_, _ = fmt.Fprintf(&sb, "      %s = %q\n", hclIdent(k), req.Tags[k])
	}
	sb.WriteString("    }\n  }\n}\n\n")

	blocks := arch.Blocks
	sort.Slice(blocks, func(i, j int) bool { return blocks[i].ID < blocks[j].ID })

	for _, b := range blocks {
		if !availableModules[b.TemplateID] {
			// Reached only if the catalog and the shipped modules disagree.
			// Failing loudly beats emitting HCL that points at nothing.
			return "", fmt.Errorf("no terraform module is bundled for template %q", b.TemplateID)
		}

		_, _ = fmt.Fprintf(&sb, "module %q {\n  source = \"./modules/%s\"\n\n",
			moduleName(b.ID), b.TemplateID)
		_, _ = fmt.Fprintf(&sb, "  name_suffix = %q\n", nameSuffix(req.ChatID))

		for _, k := range sortedKeys(b.Parameters) {
			lit, err := hclLiteral(b.Parameters[k])
			if err != nil {
				return "", fmt.Errorf("block %s parameter %s: %w", b.ID, k, err)
			}
			_, _ = fmt.Fprintf(&sb, "  %s = %s\n", hclIdent(k), lit)
		}
		sb.WriteString("}\n\n")
	}

	for _, b := range blocks {
		_, _ = fmt.Fprintf(&sb, "output %q {\n  value = module.%s\n}\n\n",
			moduleName(b.ID), moduleName(b.ID))
	}

	return sb.String(), nil
}

// hclLiteral renders a validated parameter as an HCL literal.
//
// Only the four JSON scalar shapes are accepted. Anything else is an error
// rather than a best-effort string, so a parameter type the catalog does not
// produce cannot become an unquoted expression in the output.
func hclLiteral(v any) (string, error) {
	switch t := v.(type) {
	case string:
		return fmt.Sprintf("%q", t), nil
	case bool:
		return fmt.Sprintf("%t", t), nil
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t)), nil
		}
		return fmt.Sprintf("%g", t), nil
	case json.Number:
		return t.String(), nil
	case nil:
		return "null", nil
	default:
		return "", fmt.Errorf("unsupported parameter type %T", v)
	}
}

// hclIdent strips anything that is not a valid HCL identifier character.
//
// Defence in depth: catalog.Validate already rejects unknown parameter names,
// so a key reaching here is one of a fixed set from the template definitions.
// This exists so that even a catalog authoring mistake cannot inject syntax.
func hclIdent(s string) string {
	var out strings.Builder
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
			out.WriteRune(r)
		case i > 0 && (r >= '0' && r <= '9' || r == '-'):
			out.WriteRune(r)
		}
	}
	if out.Len() == 0 {
		return "invalid"
	}
	return out.String()
}

func moduleName(blockID string) string { return hclIdent(strings.ReplaceAll(blockID, "-", "_")) }

// nameSuffix derives a short, stable, globally-unique-enough suffix from the
// chat id. Short because an ALB name caps at 32 characters; stable because a
// changing suffix would rename resources on every apply.
func nameSuffix(chatID string) string {
	sum := sha256.Sum256([]byte("ai-aws-architect/name/" + chatID))
	return hex.EncodeToString(sum[:4])
}

func sessionName(req runner.Request) string {
	s := "aiarch-" + req.DeploymentID
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
