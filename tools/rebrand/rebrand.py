#!/usr/bin/env python3
"""
Fleet -> AutoMDM rebrand.

Copies a fleetdm/fleet checkout into a destination tree while renaming:
  * the brand:   Fleet / fleet / FLEET / FleetDM / fleetctl / fleetd ...  ->  AutoMDM / automdm / AUTOMDM ...
  * the concept: fleet(s) meaning a group of hosts (Fleet 5's new name for teams)  ->  team(s)

Usage:
  rebrand.py SRC DST [--copy-only] [--domain automdm.com] [--module github.com/anilyagiz/automdm/v4]
                     [--repo github.com/anilyagiz/automdm] [--overrides overrides.tsv]...
                     [--rename-reverse-dns] [--rename-service-endpoints]
                     [--report report.txt] [--log decisions.tsv]

  --copy-only   copy SRC to DST with the same exclusions and parked paths but without renaming anything
                (used for the "upstream snapshot" commit that the rebrand commit is diffed against).

The script is deterministic: run it again on a newer upstream checkout to re-apply the same rules.
"""
import argparse
import bisect
import collections
import os
import re
import shutil
import subprocess
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
try:
    import phrases  # optional phrase rules for prose, see phrases.py
except ImportError:  # pragma: no cover
    phrases = None

# --------------------------------------------------------------------------------------------------------------------
# Scope
# --------------------------------------------------------------------------------------------------------------------

# Fleet Device Management Inc.'s company content, not part of the product.
EXCLUDE_DIRS = ("website/", "handbook/", "articles/", "it-and-security/")

# Upstream automation that must not start running in a new repository: moved aside, still rebranded.
PARKED = (
    (".github/workflows/", ".github/workflows-upstream/"),
    (".github/dependabot.yml", ".github/dependabot.upstream.yml"),
    ("CODEOWNERS", "CODEOWNERS.upstream"),
)

# Copied byte for byte: legal text, lockfiles and history.
PRESERVE_NAMES = {"LICENSE", "LICENSE.md", "LICENSE.txt", "COPYING", "NOTICE", "go.sum", "yarn.lock",
                  "package-lock.json", "pnpm-lock.yaml", "Cargo.lock", "CHANGELOG.md"}
PRESERVE_DIRS = ("changes/",)  # upstream changelog fragments

GO_EXT = {".go"}
TS_EXT = {".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".scss", ".css", ".html", ".hbs", ".ejs", ".vue", ".py"}
PROSE_EXT = {".md", ".mdx", ".txt", ".rst", ""}
JSON_EXT = {".json"}
YAML_EXT = {".yml", ".yaml"}
# everything else (sh, Makefile, Dockerfile, tf, ps1, xml, plist, sql, ...) is "config": a lone lowercase
# "fleet" there is almost always a binary, service, user or database name, so it is the brand.

# --------------------------------------------------------------------------------------------------------------------
# Word lists for telling the brand from the host-group concept
# --------------------------------------------------------------------------------------------------------------------

# fleet + <next> means a host group: fleet_id, FleetName, fleet-users, created_fleet...
TEAM_NEXT = {"id", "ids", "name", "names", "uuid", "role", "roles", "users", "user", "scoped", "selector", "dropdown",
             "picker", "filter", "filters", "member", "members", "membership", "memberships", "column", "kind", "slug",
             "spec", "specs", "admin", "admins", "maintainer", "maintainers", "observer", "observers", "technician",
             "technicians", "count", "counts", "list", "summary", "details", "detail", "switcher",
             "options", "transfer", "assignment", "assignments", "level", "less",
             "tab", "tabs", "card", "row", "rows", "table", "modal", "form", "header", "permissions"}
# <prev> + fleet means a host group: macos_fleet, default_fleet, switch-fleet, deleted_fleet...
TEAM_PREV = {"macos", "ios", "ipados", "byod", "default", "available", "current", "selected", "new", "add", "added",
             "create", "created", "edit", "edited", "delete", "deleted", "remove", "removed", "rename", "renamed",
             "switch", "single", "transfer", "transferred", "target", "targeted", "no", "all", "other", "another",
             "per", "reserved", "each", "every", "primary", "destination", "source", "original", "previous",
             "existing", "chosen", "assign", "assigned", "multi", "cross", "sub", "inter", "spec", "user", "role",
             "my", "your", "their", "this", "that", "the", "a", "one", "same", "different", "wrong", "specific",
             "own", "updated", "update", "global", "parent", "child", "empty", "unassigned", "apply", "applied",
             "move", "moved", "join", "joined", "leave", "pick", "select", "choose", "get", "list", "set", "for",
             "of", "by", "in", "on", "from", "into", "with", "without", "any", "some", "which", "what", "whose",
             "first", "last", "next", "only", "its", "his", "her", "our", "base"}
# fleet + <next> that is still the product even though the word before looks like a group word:
# "connected to Fleet MDM", "assigned to Fleet (in ABM)", "the Fleet server"
BRAND_NEXT = {"server", "servers", "instance", "instances", "binary", "binaries", "api", "cli", "agent", "agents",
              "desktop", "ui", "mdm", "url", "urls", "uri", "deployment", "deployments", "database", "db",
              "serve", "prepare", "version", "vuln", "config", "maintained", "premium", "free", "cloud",
              "variable", "variables", "var", "vars", "osquery", "documentation", "docs", "web",
              "support", "certificate", "release", "releases", "update", "updates", "packages", "package",
              "installer", "installers", "app", "apps", "mcp", "gitops", "initiated", "managed",
              "enrollment", "enroll", "challenge", "ca", "root", "token", "tokens", "process", "service",
              "services", "logo", "icon", "theme", "black", "white", "blue", "green", "purple", "red",
              "checkbox", "slackbot", "account", "dm", "ctl", "d", "client", "clients", "http", "error", "errors",
              "platform", "platforms", "file", "files", "profile", "profiles", "payload", "payloads", "identifier",
              "cert", "certs", "key", "keys", "signing", "scep", "ndes", "idp", "sso", "saml", "log", "logs",
              "logger", "metrics", "endpoint", "endpoints", "request", "response", "handler", "session", "sessions",
              "auth", "login", "license", "secret", "secrets", "env", "test", "tests", "integration", "dev",
              "example", "demo", "sandbox"}
# "Fleet <next>" in UI copy and messages where the capitalised word still means a group of hosts
CAP_TEAM_NEXT = {"name", "names", "id", "ids", "column", "dropdown", "switcher", "filter", "filters", "picker",
                 "selector", "assignment", "assignments", "row", "specs"}
# "<prev> fleet" in descriptions inside YAML/JSON/config values: a determiner means a group of hosts
DETERMINERS = {"a", "the", "this", "that", "each", "every", "per", "no", "another", "any", "one", "same", "new",
               "default", "specific", "single", "which", "current", "selected", "target", "destination", "other",
               "their", "its", "whose"}
HELM_COMPOSE_RE = re.compile(r"(?:^|/)(?:charts|helm[^/]*|helm-values)/|(?:^|/)docker-compose[^/]*\.ya?ml$|"
                             r"(?:^|/)values[^/]*\.ya?ml$|(?:^|/)compose[^/]*\.ya?ml$")
# CLI subcommands of the `fleet` server binary
SERVER_SUBCOMMANDS = {"serve", "prepare", "version", "vuln_processing", "config_dump", "help", "--help", "-h"}

# fleet<rest> joined lowercase words that are not the brand
JOINED_KEEP = {"ing", "ingly", "ie", "ies", "er", "ers", "ly", "ness", "wide", "smith", "wood", "foot"}
JOINED_TEAM = {"id", "ids", "name", "names", "s", "less"}

FILE_EXT_RE = re.compile(
    r"\.(?:ya?ml|json|go|ts|tsx|js|jsx|sh|bash|conf|cfg|ini|toml|env|exe|pkg|msi|deb|rpm|dmg|app|zip|tar|gz|tgz|"
    r"service|plist|log|db|sql|crt|key|pem|pub|png|svg|jpe?g|gif|ico|icns|webp|mp4|txt|md|html|css|scss|tmpl|"
    r"xml|wxs|bat|ps1|sock|pid|mod|sum|lock|mobileconfig|rb|py)\b")
DOMAIN_AFTER_RE = re.compile(r"(?:\.[a-z][a-z0-9-]*)*\.(?:com|io|org|net|app|dev|co|local|internal|example|test)\b")

# --------------------------------------------------------------------------------------------------------------------
# Protected strings: kept as they are
# --------------------------------------------------------------------------------------------------------------------

PROTECT_ALWAYS = [
    # references to upstream issues, PRs, discussions, advisories and commits stay valid links
    r"github\.com/fleetdm/fleet/(?:issues|pull|discussions|security|commit|compare|releases/tag)\b[^\s)\"'`>\]]*",
    r"github\.com/fleetdm/fleet#\d+",
    r"\bfleetdm/fleet#\d+",
    # other repositories and images of the fleetdm organization: dependencies, data feeds and build images
    r"\bfleetdm/(?!fleet(?:ctl)?(?:\.git)?(?![A-Za-z0-9_-]))[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)*",
    # the GitHub organization that publishes the CPE, OVAL and vulnerability feeds
    r'"fleetdm",(?=\s*"(?:nvd|vulnerabilities)")',
    r'(?<=owner)(\s*=\s*)"fleetdm"',
    r'(?<=githubOwner)(\s*=\s*)"fleetdm"',
    # CPE names are NVD's, not ours
    r"cpe:2\.3:[aoh]:fleetdm:[A-Za-z0-9_.:*-]*",
    r"cpe:/[aoh]:fleetdm:[A-Za-z0-9_.:*-]*",
    # legal entity names
    r"Fleet Device Management,? Inc\.?",
    r"Fleet Device Management",
]
# operational endpoints that AutoMDM must host before they can move (see REBRAND.md)
PROTECT_SERVICE = [
    r"\b(?:updates|updates-staging|download|download-testing|download-test|maintained-apps|dogfood|chrome|"
    r"chrome-beta|sso|free|blog|license|[a-z0-9-]+\.cloud|[a-z0-9-]*\.?loadtest|otlp\.signoz\.dogfood)"
    r"\.fleetdm\.com\b",
    r"\btuf\.fleetctl\.com\b",
    r"(?<![A-Za-z0-9.-])(?:www\.)?fleetdm\.com/api/[^\s\"'`)>\]]*",
]
# reverse-DNS identifiers: Apple payload identifiers, bundle ids, launchd labels, Android application ids
PROTECT_REVERSE_DNS = [
    r"\bcom\.fleet(?:dm)?(?![A-Za-z0-9])(?:[.-][A-Za-z0-9_]+)*",
    r"(?<![A-Za-z0-9._-])com/fleetdm\b",
    r"\bcom_fleetdm_[A-Za-z0-9_]+",
]

WORDCH = frozenset("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-")
B64CH = frozenset("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789+/=")
SUBWORD_RE = re.compile(r"[A-Z]{2,}s(?![a-z])|[A-Z]+(?=[A-Z][a-z])|[A-Z]?[a-z]+|[A-Z]+|[0-9]+")
FLEET_EXACT = re.compile(r"fleet|Fleet|FLEET")
IDENT_RE = re.compile(r"[A-Za-z_][A-Za-z0-9_]*")
WORD_BEFORE_RE = re.compile(r"([A-Za-z][A-Za-z'-]*)[ \t]*$")
WORD_AFTER_RE = re.compile(r"[ \t]+([A-Za-z_-]+)")
SPLIT_SEP_RE = re.compile(r"([_-]+)")
# "a Fleet server" became "a AutoMDM server": the brand now starts with a vowel sound
ARTICLE_RE = re.compile(r"\b([Aa]) ((?:\*\*|[\[*_`\"'(])?(?:<b>|<strong>|<em>|<code>)?(?:AutoMDM|automdm))")
HEX2_RE = re.compile(r"[0-9A-Fa-f]{2}")
HEX4_RE = re.compile(r"[0-9A-Fa-f]{4}")

BRAND = "automdm"
TEAM = "team"


def split_compound(compound):
    """Split into a list of (text, is_word). Separators keep their text."""
    out = []
    for part in SPLIT_SEP_RE.split(compound):
        if not part:
            continue
        if part[0] in "_-":
            out.append((part, False))
            continue
        pos = 0
        for m in SUBWORD_RE.finditer(part):
            if m.start() > pos:
                out.append((part[pos:m.start()], True))
            out.append((m.group(0), True))
            pos = m.end()
        if pos < len(part):
            out.append((part[pos:], True))
    return out


def cased(orig, base, rest=""):
    """Render base(+rest) in the case style of orig. base is 'automdm' or 'team'."""
    if len(orig) > 1 and orig.isupper():
        return (base + rest).upper()
    if orig[0].isupper():
        head = "AutoMDM" if base == BRAND else "Team"
        return head + rest
    return base + rest


def parts_adjacent(parts, word_index):
    """True when word number word_index is directly followed by another word (no separator between them)."""
    wi = -1
    for k, (_, is_word) in enumerate(parts):
        if is_word:
            wi += 1
            if wi == word_index:
                return k + 1 < len(parts) and parts[k + 1][1]
    return False


class Ctx:
    """Where in a file a compound sits."""
    __slots__ = ("kind", "region", "before", "after", "line", "path", "lineno", "idx")

    def __init__(self, kind, region, before, after, line, path="", lineno=0, idx=0):
        self.kind, self.region, self.before, self.after, self.line = kind, region, before, after, line
        self.path, self.lineno, self.idx = path, lineno, idx


class Rebrander:
    def __init__(self, args):
        self.domain = args.domain
        self.module = args.module
        self.repo = args.repo
        pats = list(PROTECT_ALWAYS)
        if not args.rename_service_endpoints:
            pats += PROTECT_SERVICE
        if not args.rename_reverse_dns:
            pats += PROTECT_REVERSE_DNS
        self.protect_re = re.compile("|".join(f"(?:{p})" for p in pats))
        self.ident_tokens = set()       # every identifier-like token in code files (for collision checks)
        self.decisions = {}             # camelCase identifier -> replacement
        self.code_map = collections.defaultdict(set)   # new identifier -> {old identifiers} (go/ts code)
        self.stats = collections.Counter()
        self.review = collections.defaultdict(list)  # reason -> [(path, line_no, line)]
        self.overrides = {}             # (path, lineno) -> {idx or '*': decision}
        self.used_overrides = set()
        self.log = None
        for fn in args.overrides:
            self.load_overrides(fn)

    def load_overrides(self, fn):
        with open(fn, encoding="utf-8") as fh:
            for raw in fh:
                raw = raw.rstrip("\n")
                if not raw or raw.startswith("#"):
                    continue
                cols = raw.split("\t")
                if len(cols) < 4:
                    continue
                path, lineno, idx, decision = cols[0], int(cols[1]), cols[2].strip(), cols[3].strip()
                if decision not in ("brand", "team", "keep"):
                    raise SystemExit(f"bad override decision in {fn}: {raw}")
                self.overrides.setdefault((path, lineno), {})[idx] = decision

    # ---------------------------------------------------------------------------------------------- classification

    def classify(self, words, i, ctx, lone):
        """Return 'brand', 'team' or 'keep' for words[i], which contains 'fleet'."""
        w = words[i]
        lw = w.lower()
        prev = words[i - 1].lower() if i > 0 else None
        nxt = words[i + 1].lower() if i + 1 < len(words) else None

        if lw == "fleetdm":
            return "brand"
        if lw != "fleet" and lw != "fleets":
            if not lw.startswith("fleet"):
                return "keep"
            rest = lw[5:]
            if rest in JOINED_KEEP:
                return "keep"
            if rest in JOINED_TEAM:
                return "team"
            return "brand"
        if lone:
            return self.classify_lone(w, ctx)
        if lw == "fleets":
            return "team"
        # exactly "fleet" (any case) inside a longer identifier
        if nxt == "wide":
            return "keep"                                    # fleet-wide: the generic English word
        if nxt in BRAND_NEXT:
            return "brand"
        if nxt in TEAM_NEXT:
            return "team"
        if prev in TEAM_PREV:
            return "team"
        return "brand"

    def classify_lone(self, w, ctx):
        before, after, kind, region = ctx.before, ctx.after, ctx.kind, ctx.region
        if w.isupper() and len(w) > 1:
            return "brand"                                   # FLEET env prefix, FLEET_ constants
        plural = w.lower() == "fleets"
        # paths, URLs, import paths, hostnames: /api/v1/fleet/, server/fleet, fleetdm/fleet, fleet.example.com
        if not plural:
            if before.endswith("/") or after.startswith("/") or DOMAIN_AFTER_RE.match(after):
                return "brand"
            if FILE_EXT_RE.match(after):
                return "brand"                               # file names
            if before.endswith("@") or before.endswith("://") or after.startswith("://"):
                return "brand"
        m = WORD_AFTER_RE.match(after)
        nxt_word = m.group(1).lower() if m else None
        prose_like = kind == "prose" and region == "prose" or kind == "ts" or \
            (kind == "go" and region in ("string", "comment")) or region == "comment"
        if prose_like and phrases is not None:
            mb = WORD_BEFORE_RE.search(before)
            prev_word = mb.group(1).lower() if mb else None
            d = phrases.decide(w, prev_word, nxt_word, before, after, kind, region)
            if d:
                return d
        if plural:
            return "team"
        if nxt_word in SERVER_SUBCOMMANDS or nxt_word in BRAND_NEXT:
            return "brand"
        if w[0].isupper():
            if after.startswith("'s") or after.startswith("’s"):
                return "brand"                               # Fleet's
            if nxt_word in CAP_TEAM_NEXT and (region != "code" or kind == "ts"):
                return "team"                                # "Fleet name", "Fleet column"
            return "brand"                                   # Fleet is the product

        # lowercase "fleet"
        if before.endswith('"') and re.match(r'"\s*:', after):
            return "team"                                    # {"fleet": ...} JSON key, in any kind of file
        if before.endswith("--") and not before.endswith("---"):
            return "team"                                    # --fleet flag
        if before.endswith("-D") or before.endswith("-u"):
            return "brand"                                   # mysql -Dfleet -ufleet
        a0, b0 = after[:1], before[-1:]
        if kind != "ts" and a0 == "." and after[1:2].isalpha():
            return "brand"                                   # fleet.Datastore, fleet.host_cache.lookups
        if region != "prose" and kind != "ts":
            if a0 == ":" and after[1:2].isalnum():
                return "brand"                               # fleet:hostcache:v2, fleet:json
            if a0 in ("-", "_"):
                return "brand"                               # "fleet-" + id, fleet_ prefix
            if b0 == "_":
                return "team"                                # "_fleet" key suffix, as in macos_fleet
            if b0 == "-" and not before.endswith("--"):
                return "brand"                               # "-fleet" name suffix
        if kind in ("yaml", "json", "config") and region != "comment":
            mb = WORD_BEFORE_RE.search(before)
            if mb and mb.group(1).lower() in DETERMINERS:
                return "team"                                # description text: "Create a fleet"
        if kind == "go":
            if region == "code":
                return "brand"                               # package qualifier fleet.X, package fleet, local vars
            if region == "tag":
                return "team"                                # json:"fleet"
            if region == "string" and before.endswith('"') and after.startswith('"'):
                line = ctx.line
                if re.search(r'json:|WithString\(|getOptionalString\(|Kind\b|TranslatorType|"fleets"|TeamName|'
                             r'Team\b|Teams\b|team', line):
                    return "team"
                return "brand"                               # "fleet" as a database, user, meter or namespace name
            return "team"                                    # prose in comments and messages
        if kind == "ts":
            return "team"                                    # JSON keys, UI copy, variables for a host group
        if kind == "json":
            return "brand"
        if kind == "yaml":
            last_line = before.rsplit("\n", 1)[-1]
            if after.startswith(":") and re.fullmatch(r"\s*(?:-\s*)?", last_line):
                if HELM_COMPOSE_RE.search(ctx.path):
                    return "brand"                           # helm values and compose service names
                return "team"                                # fleet: key in an automdmctl spec
            if re.search(r"kind:\s*$", before):
                return "team"                                # kind: fleet
            if region == "comment":
                return "team"
            return "brand"
        if kind == "prose":
            if region in ("fence", "span"):
                last_line = before.rsplit("\n", 1)[-1]
                if after.startswith(":") and re.fullmatch(r"\s*(?:-\s*)?", last_line):
                    return "team"                            # fleet: key in a YAML example
                return "brand"                               # commands, binary, database and user names
            if before.endswith("./") or re.search(r"\$\s*$", before):
                return "brand"                               # ./fleet, $ fleet ...
            return "team"
        if kind == "path":
            return "brand"
        # config files: shell, Makefile, Dockerfile, terraform, sql ...
        if region == "comment":
            return "team"
        return "brand"

    # ---------------------------------------------------------------------------------------------- compound rewrite

    def rewrite_compound(self, compound, ctx):
        parts = split_compound(compound)
        words = [p for p, is_word in parts if is_word]
        has_sep = len(words) != len(parts)
        lone = len(words) == 1
        forced = None
        if self.overrides and ctx.path:
            ov = self.overrides.get((ctx.path, ctx.lineno))
            if ov:
                forced = ov.get(str(ctx.idx)) or ov.get("*")
                if forced:
                    self.used_overrides.add((ctx.path, ctx.lineno))
                    if forced == "keep":
                        self.stats["override: keep"] += 1
                        return compound
        out_words = []
        changed_kinds = set()
        skip = False
        for i, w in enumerate(words):
            if skip:
                out_words.append("")
                skip = False
                continue
            lw = w.lower()
            if "fleet" not in lw:
                out_words.append(w)
                continue
            if not lw.startswith("fleet") and w.islower() and ("fleetctl" in w or "fleetdm" in w):
                out_words.append(w.replace("fleetctl", "automdmctl").replace("fleetdm", "automdm"))
                changed_kinds.add("brand")
                continue
            # FleetDM written as two camel words
            if lw == "fleet" and i + 1 < len(words) and words[i + 1] == "DM" and parts_adjacent(parts, i):
                out_words.append(cased(w, BRAND))
                skip = True
                changed_kinds.add("brand")
                continue
            c = self.classify(words, i, ctx, lone)
            if lone and self.log is not None and lw in ("fleet", "fleets"):
                self.log_decision(w, forced or c, ctx)
            if c != "keep" and forced:
                c = forced
            if c == "keep":
                out_words.append(w)
                continue
            changed_kinds.add(c)
            base = BRAND if c == "brand" else TEAM
            if lw == "fleetdm":
                out_words.append(cased(w, BRAND))
            elif lw == "fleets":
                out_words.append(cased(w, base, "S" if w.isupper() else "s"))
            elif lw == "fleet":
                out_words.append(cased(w, base))
            else:  # joined: fleetctl, Fleetd, fleetid ...
                out_words.append(cased(w, base, w[5:]))
        # rebuild with the original separators
        res, wi = [], 0
        for p, is_word in parts:
            if is_word:
                res.append(out_words[wi])
                wi += 1
            else:
                res.append(p)
        new = "".join(res)
        if new == compound:
            return compound

        is_code = ctx.kind in ("go", "ts", "path")
        # identifiers written in camelCase/PascalCase: renaming one to "team..." could collide with an existing
        # identifier, so use the brand instead whenever the team spelling already exists anywhere.
        if not has_sep and not lone and "team" in changed_kinds and is_code and not forced:
            if compound in self.decisions:
                new = self.decisions[compound]
            else:
                if new in self.ident_tokens:
                    new = self.force_brand(words)
                    self.stats["team->brand (collision avoided)"] += 1
                self.decisions[compound] = new
        if is_code and ctx.region in ("code", "tag") and not has_sep:
            self.code_map[new].add(compound)
        for k in changed_kinds:
            self.stats[k] += 1
        return new

    @staticmethod
    def force_brand(words):
        out = []
        skip = False
        for i, w in enumerate(words):
            if skip:
                skip = False
                continue
            lw = w.lower()
            if not lw.startswith("fleet") or lw[5:] in JOINED_KEEP:
                out.append(w)
            elif lw == "fleet" and i + 1 < len(words) and words[i + 1] == "DM":
                out.append(cased(w, BRAND))
                skip = True
            elif lw == "fleets":
                out.append(cased(w, BRAND, "S" if w.isupper() else "s"))
            elif lw == "fleetdm":
                out.append(cased(w, BRAND))
            else:
                out.append(cased(w, BRAND, w[5:]))
        return "".join(out)

    def log_decision(self, w, decision, ctx):
        mb = WORD_BEFORE_RE.search(ctx.before)
        ma = WORD_AFTER_RE.match(ctx.after)
        prev = mb.group(1).lower() if mb else ""
        nxt = ma.group(1).lower() if ma else ""
        line = ctx.line.strip().replace("\t", " ").replace("\r", " ")
        if len(line) > 220:
            line = line[:220]
        adj = (ctx.before[-3:] + "|" + ctx.after[:3]).replace("\t", " ").replace("\n", "\u23ce").replace("\r", "\u23ce")
        self.log.write(f"{ctx.kind}\t{ctx.region}\t{decision}\t{w}\t{prev}\t{nxt}\t{ctx.path}\t{ctx.lineno}\t"
                       f"{ctx.idx}\t{line}\t{adj}\n")

    # ---------------------------------------------------------------------------------------------- regions

    GO_TOK = re.compile(r'//|/\*|`|"|\'')
    GO_TAG = re.compile(r'`\s*(?:json|db|csv|yaml|url|query|form|renameto|removedkey|mapstructure|xml|plist|toml)'
                        r':"')

    @classmethod
    def go_regions(cls, text):
        """Return [(start, end, region)] covering text. Regions: code, comment, string, tag."""
        out = []
        n = len(text)
        start = 0
        i = 0
        while True:
            m = cls.GO_TOK.search(text, i)
            if not m:
                break
            i = m.start()
            t = m.group(0)
            if t == "//":
                j = text.find("\n", i)
                j = n if j < 0 else j
                region = "comment"
            elif t == "/*":
                j = text.find("*/", i + 2)
                j = n if j < 0 else j + 2
                region = "comment"
            elif t == "`":
                j = text.find("`", i + 1)
                j = n if j < 0 else j + 1
                region = "tag" if cls.GO_TAG.match(text, i) else "string"
            else:
                j = i + 1
                while j < n:
                    c = text[j]
                    if c == "\\":
                        j += 2
                        continue
                    if c == t or c == "\n":
                        break
                    j += 1
                j = min(j + 1, n)
                region = "string"
            if i > start:
                out.append((start, i, "code"))
            out.append((i, j, region))
            i = start = j
        if start < n:
            out.append((start, n, "code"))
        return out

    PROSE_CODE = re.compile(r"(?ms)^[ \t]*(```|~~~).*?^[ \t]*\1[ \t]*$|`[^`\n]+`")
    HASH_COMMENT = re.compile(r"(?m)(?:^|[ \t])#[^\n]*")

    @classmethod
    def simple_regions(cls, text, kind):
        n = len(text)
        if kind in ("yaml", "config"):
            out, pos = [], 0
            for m in cls.HASH_COMMENT.finditer(text):
                if m.start() > pos:
                    out.append((pos, m.start(), "code"))
                out.append((m.start(), m.end(), "comment"))
                pos = m.end()
            if pos < n:
                out.append((pos, n, "code"))
            return out
        if kind == "prose":
            out, pos = [], 0
            # fenced blocks and inline code spans
            for m in cls.PROSE_CODE.finditer(text):
                if m.start() > pos:
                    out.append((pos, m.start(), "prose"))
                out.append((m.start(), m.end(), "fence" if m.group(1) else "span"))
                pos = m.end()
            if pos < n:
                out.append((pos, n, "prose"))
            return out
        return [(0, n, "code")]

    # ---------------------------------------------------------------------------------------------- text rewrite

    def fixed_rewrites(self, text, protected):
        text = text.replace("github.com/fleetdm/fleet/v4", self.module)

        def protect(m):
            protected.append(m.group(0))
            return f"\x00P{len(protected) - 1}\x00"
        text = self.protect_re.sub(protect, text)
        text = text.replace("github.com/fleetdm/fleet", self.repo)
        text = re.sub(r"(?<![A-Za-z0-9.-])(?:www\.)?fleetdm\.com\b", self.domain, text)
        return text

    def rewrite_text(self, text, kind, path):
        protected = []
        text = self.fixed_rewrites(text, protected)
        text = re.sub(r"(?<![A-Za-z0-9])Fleet MDM\b", "AutoMDM", text)
        if not FLEET_EXACT.search(text) and "AutoMDM" not in text and "automdm" not in text:
            return self.unprotect(text, protected)

        regions = self.go_regions(text) if kind == "go" else self.simple_regions(text, kind)
        line_starts = [0]
        find = text.find
        p = find("\n")
        while p >= 0:
            line_starts.append(p + 1)
            p = find("\n", p + 1)
        line_idx = {}
        out = []
        for start, end, region in regions:
            if FLEET_EXACT.search(text, start, end):
                chunk = self.rewrite_chunk(text, start, end, region, kind, path, line_starts, line_idx)
            else:
                chunk = text[start:end]
            if kind == "go" and region in ("comment", "string") and ("AutoMDM" in chunk or "automdm" in chunk):
                chunk = ARTICLE_RE.sub(r"\1n \2", chunk)
            out.append(chunk)
        text = "".join(out)
        if kind != "go":
            text = ARTICLE_RE.sub(r"\1n \2", text)
        # Go canonicalizes header names ("X-Automdm-Capabilities"); code that indexes an http.Header directly needs that form.
        text = text.replace("X-AutoMDM-", "X-Automdm-")
        return self.unprotect(text, protected)

    def rewrite_chunk(self, text, start, end, region, kind, path, line_starts, line_idx):
        out = []
        pos = start
        n = len(text)
        for m in FLEET_EXACT.finditer(text, start, end):
            s, e = m.span()
            if s < pos:
                continue
            a = s
            lo = max(pos, s - 300)
            while a > lo and text[a - 1] in WORDCH:
                a -= 1
            b = e
            hi = min(end, e + 300)
            while b < hi and text[b] in WORDCH:
                b += 1
            if s - a >= 300 or b - e >= 300:
                self.stats["skipped: inside a very long token"] += 1
                continue
            # base64 and similar blobs: a long unbroken run of base64 characters with few slashes around the match
            ba, bb = s, e
            while ba > start and s - ba < 130 and text[ba - 1] in B64CH:
                ba -= 1
            while bb < end and bb - e < 130 and text[bb] in B64CH:
                bb += 1
            if bb - ba >= 120 and text.count("/", ba, bb) * 30 < bb - ba:
                self.stats["skipped: inside a base64-like blob"] += 1
                self.review["skipped base64-like blob"].append((path, 0, text[ba:ba + 80]))
                continue
            # an escape (\n, \t, \u0026) or a url-encoded byte (%2F) glued to the front of the word is not part of it
            if a < s and a > start and region != "prose":
                pc = text[a - 1]
                if pc == "\\":
                    k, nb = a - 1, 0
                    while k >= start and text[k] == "\\":
                        nb += 1
                        k -= 1
                    if nb % 2 == 1:
                        ch = text[a]
                        if ch in "ntrbfv":
                            a += 1
                        elif ch == "u" and a + 5 <= s and HEX4_RE.match(text, a + 1):
                            a += 5
                        elif ch == "x" and a + 3 <= s and HEX2_RE.match(text, a + 1):
                            a += 3
                elif pc == "%" and a + 2 <= s and HEX2_RE.match(text, a):
                    a += 2
                elif pc == "-" and text[a] == "D" and a + 1 == s:
                    a += 1                                   # mysql -Dfleet
            while a < s and text[a] in "_-":
                a += 1
            while b > e and text[b - 1] in "_-":
                b -= 1
            comp = text[a:b]
            li = bisect.bisect_right(line_starts, a) - 1
            ls = line_starts[li]
            le = line_starts[li + 1] - 1 if li + 1 < len(line_starts) else n
            idx = line_idx.get(li, 0)
            line_idx[li] = idx + 1
            ctx = Ctx(kind, region, text[max(0, a - 40):a], text[b:b + 40], text[ls:le], path, li + 1, idx)
            new = self.rewrite_compound(comp, ctx)
            out.append(text[pos:a])
            out.append(new)
            pos = b
        out.append(text[pos:end])
        return "".join(out)

    @staticmethod
    def unprotect(text, protected):
        if not protected:
            return text
        return re.sub(r"\x00P(\d+)\x00", lambda m: protected[int(m.group(1))], text)

    def rewrite_path(self, path):
        path = park(path)
        protected = []
        path = self.fixed_rewrites(path, protected)
        if not FLEET_EXACT.search(path):
            return self.unprotect(path, protected)
        parts = path.split("/")
        out = []
        for i, p in enumerate(parts):
            if not FLEET_EXACT.search(p):
                out.append(p)
                continue
            before = "/".join(parts[:i]) + ("/" if i else "")
            text = before + p + ("/" if i < len(parts) - 1 else "")
            out.append(self.rewrite_chunk(text, len(before), len(before) + len(p), "path", "path", "", [0], {}))
        return self.unprotect("/".join(out), protected)


# --------------------------------------------------------------------------------------------------------------------
# Post-processing for Fleet 5's teams -> fleets compatibility layer
# --------------------------------------------------------------------------------------------------------------------

def fix_go_aliases(text):
    """After the rename some alias tags point a key to itself: json:"team_id" renameto:"team_id". Drop them."""
    def drop(m):
        return m.group(1) if m.group(2) == m.group(3) else m.group(0)
    text = re.sub(r'(json:"([^",]*)[^"]*")[ \t]+renameto:"([^",]*)(?:,inline)?"', drop, text)
    text = re.sub(r'(json:"([^",]*)[^"]*")[ \t]+removedkey:"([^"]*)"', drop, text)
    return text


# --------------------------------------------------------------------------------------------------------------------
# Driver
# --------------------------------------------------------------------------------------------------------------------

def file_kind(path):
    base = os.path.basename(path)
    ext = os.path.splitext(base)[1].lower()
    if ext in GO_EXT:
        return "go"
    if ext in TS_EXT:
        return "ts"
    if ext in JSON_EXT:
        return "json"
    if ext in YAML_EXT:
        return "yaml"
    if ext in PROSE_EXT and base not in ("Makefile", "Dockerfile") and not base.startswith("Dockerfile"):
        return "prose"
    return "config"


def is_text(data):
    if b"\x00" in data[:8192]:
        return False
    try:
        data.decode("utf-8")
        return True
    except UnicodeDecodeError:
        return False


def park(path):
    for old, new in PARKED:
        if path == old or (old.endswith("/") and path.startswith(old)):
            return new + path[len(old):]
    return path


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("src")
    ap.add_argument("dst")
    ap.add_argument("--copy-only", action="store_true")
    ap.add_argument("--domain", default="automdm.com")
    ap.add_argument("--module", default="github.com/anilyagiz/automdm/v4")
    ap.add_argument("--repo", default="github.com/anilyagiz/automdm")
    ap.add_argument("--overrides", action="append", default=[])
    ap.add_argument("--rename-reverse-dns", action="store_true",
                    help="also rename com.fleetdm.* identifiers (Apple payload ids, bundle ids, Android app id)")
    ap.add_argument("--rename-service-endpoints", action="store_true",
                    help="also rename the fleetdm.com service hosts the product calls (updates, downloads, APIs)")
    ap.add_argument("--report", default=None)
    ap.add_argument("--log", default=None, help="write one line per lone fleet/fleets word with its decision")
    ap.add_argument("--only", default=None, help="only process paths with this prefix (for quick experiments)")
    ap.add_argument("--dry-run", action="store_true", help="classify and log only; write no output files")
    args = ap.parse_args()

    files = subprocess.run(["git", "-C", args.src, "ls-files", "-z"], capture_output=True, check=True
                           ).stdout.decode().split("\0")
    files = [f for f in files if f and not f.startswith(EXCLUDE_DIRS)]

    if args.copy_only:
        for f in files:
            src = os.path.join(args.src, f)
            dst = os.path.join(args.dst, park(f))
            os.makedirs(os.path.dirname(dst) or ".", exist_ok=True)
            if os.path.islink(src):
                os.symlink(os.readlink(src), dst)
            elif os.path.isfile(src):
                shutil.copy2(src, dst)
        print(f"copied {len(files)} files")
        return 0

    rb = Rebrander(args)
    if args.log:
        rb.log = open(args.log, "w", encoding="utf-8")

    # pass 1: identifier inventory for collision checks
    cache = {}
    for f in files:
        p = os.path.join(args.src, f)
        if not os.path.isfile(p) or os.path.islink(p):
            continue
        with open(p, "rb") as fh:
            data = fh.read()
        if is_text(data):
            text = data.decode("utf-8")
            if file_kind(f) in ("go", "ts"):
                rb.ident_tokens.update(IDENT_RE.findall(text))
            cache[f] = None if len(data) > 2_000_000 else text
        else:
            cache[f] = False

    # pass 2: rewrite
    renamed = {}
    seen_dst = {}
    for f in files:
        if args.only and not f.startswith(args.only):
            continue
        src = os.path.join(args.src, f)
        if os.path.islink(src):
            new_rel = rb.rewrite_path(f)
            dst = os.path.join(args.dst, new_rel)
            if not args.dry_run:
                os.makedirs(os.path.dirname(dst), exist_ok=True)
                os.symlink(rb.rewrite_path(os.readlink(src)), dst)
            continue
        if not os.path.isfile(src):
            continue
        new_rel = rb.rewrite_path(f)
        if new_rel in seen_dst:
            rb.review["path collision"].append((f, 0, f"also produced by {seen_dst[new_rel]}"))
            new_rel = park(f)
        seen_dst[new_rel] = f
        if new_rel != f:
            renamed[f] = new_rel
        dst = os.path.join(args.dst, new_rel)
        if not args.dry_run:
            os.makedirs(os.path.dirname(dst) or ".", exist_ok=True)
        base = os.path.basename(f)
        text = cache.get(f)
        preserve = (base in PRESERVE_NAMES or base.startswith("LICENSE") or f.startswith(PRESERVE_DIRS)
                    or text is False)
        if preserve:
            if not args.dry_run:
                shutil.copyfile(src, dst)
                shutil.copymode(src, dst)
            continue
        if text is None:
            with open(src, "rb") as fh:
                text = fh.read().decode("utf-8")
        kind = file_kind(f)
        new = rb.rewrite_text(text, kind, f)
        if kind == "go":
            new = fix_go_aliases(new)
        if not args.dry_run:
            with open(dst, "w", encoding="utf-8", newline="") as fh:
                fh.write(new)
            shutil.copymode(src, dst)
        rb.stats["files rewritten" if new != text else "files unchanged"] += 1
    if rb.log:
        rb.log.close()
    if args.dry_run:
        for k, v in sorted(rb.stats.items()):
            print(f"{v:8d}  {k}")
        return 0

    # leftovers
    left = collections.Counter()
    left_where = collections.defaultdict(collections.Counter)
    for root, dirs, fnames in os.walk(args.dst):
        dirs[:] = [d for d in dirs if d != ".git"]
        for fn in fnames:
            p = os.path.join(root, fn)
            rel = os.path.relpath(p, args.dst)
            if os.path.islink(p) or rel.startswith(PRESERVE_DIRS) or fn in PRESERVE_NAMES or fn.startswith("LICENSE"):
                continue
            with open(p, "rb") as fh:
                data = fh.read()
            if b"leet" not in data and b"LEET" not in data:
                continue
            if not is_text(data):
                continue
            for m in re.finditer(r"[A-Za-z0-9_.-]*(?:fleet|Fleet|FLEET)[A-Za-z0-9_.-]*", data.decode("utf-8")):
                left[m.group(0)] += 1
                left_where[m.group(0)][rel] += 1

    report = []
    report.append("== stats")
    for k, v in sorted(rb.stats.items()):
        report.append(f"{v:8d}  {k}")
    report.append(f"{len(renamed):8d}  paths renamed")
    unused = [k for k in rb.overrides if k not in rb.used_overrides]
    report.append(f"{len(rb.overrides):8d}  override lines loaded, {len(unused)} unused")
    for k in unused[:200]:
        report.append(f"  unused override: {k[0]}:{k[1]}")
    report.append("\n== code identifiers that now share one name (must be reviewed)")
    for new, olds in sorted(rb.code_map.items()):
        if len(olds) > 1:
            report.append(f"  {new} <- {sorted(olds)}")
    report.append("\n== identifiers kept as brand because the team spelling already exists")
    for k, v in sorted(rb.decisions.items()):
        if "Team" not in v and "team" not in v:
            report.append(f"  {k} -> {v}")
    report.append("\n== remaining 'fleet' strings (protected names, words like fleeting, upstream references)")
    for k, v in left.most_common(150):
        where = ", ".join(f"{p} ({c})" for p, c in left_where[k].most_common(3))
        report.append(f"{v:8d}  {k}    [{where}]")
    for reason, items in rb.review.items():
        report.append(f"\n== review: {reason} ({len(items)})")
        for f, no, ln in items[:400]:
            report.append(f"  {f}:{no}: {ln}")
    report.append("\n== renamed paths")
    for k, v in sorted(renamed.items()):
        report.append(f"  {k} -> {v}")
    out = "\n".join(report) + "\n"
    if args.report:
        with open(args.report, "w") as fh:
            fh.write(out)
    print("\n".join(report[:14]))
    return 0


if __name__ == "__main__":
    sys.exit(main())
