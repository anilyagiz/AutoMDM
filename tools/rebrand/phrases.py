"""
Phrase rules for a lone "fleet"/"Fleet"/"fleets"/"Fleets" in prose-like text (docs, comments, UI copy, messages).

decide() returns "brand", "team", "keep" or None (no opinion: the caller's defaults apply).
"keep" is for the generic English word: "your fleet of laptops", "across the whole fleet".
"""

# "<prev> fleet" where fleet means all of an organization's devices
GENERIC_PREV = {"whole", "entire", "large", "larger", "largest", "small", "smaller", "big", "bigger", "device",
                "your", "our", "company", "company's", "organization's", "organisation's", "customer's",
                "across", "vehicle", "growing", "mixed", "diverse", "global"}
# "fleet of <next>"
GENERIC_OF_NEXT = {"devices", "hosts", "laptops", "computers", "machines", "endpoints", "servers", "macs",
                   "tens", "hundreds", "thousands", "millions", "workstations", "phones", "tablets", "vehicles",
                   "n", "any", "this", "that", "over", "more", "up", "about", "roughly", "some"}
# lowercase "fleet <next>" that is the product written in lower case
BRAND_NEXT_LOWER = {"server", "servers", "instance", "instances", "repo", "repository", "install", "installation",
                    "url", "binary", "serve", "prepare", "database", "db", "desktop", "premium", "free",
                    "deployment", "container", "image", "docker", "process", "logs", "version", "upgrade",
                    "address", "responds", "source", "codebase", "monorepo", "checkout", "clone"}


def decide(w, prev, nxt, before, after, kind, region):
    lw = w.lower()
    if after[:1] == "#" and after[1:2].isdigit():
        return "keep"                    # fleet#12345: an upstream issue number
    if w == "fleet":
        if nxt == "of":
            # "a fleet of 10,000 hosts" is generic; "the fleet of the host" is a team
            tail = after.split("of", 1)[1].strip().split(" ", 1)[0].lower().strip(".,;:()") if "of" in after else ""
            if tail in GENERIC_OF_NEXT or tail[:1].isdigit():
                return "keep"
            return None
        if nxt in BRAND_NEXT_LOWER:
            return "brand"
        if prev in GENERIC_PREV:
            return "keep"
        return None
    if w == "fleets":
        if prev in ("large", "larger", "small", "big", "bigger", "device", "mixed"):
            return "keep"
        return None
    return None
