#!/usr/bin/env python3
"""Generate CGO-free Go bindings from webgpu.h and wgpu.h.

The generated declarations follow the Windows x64 C ABI. Structs are laid out
with natural alignment, which matches both the Microsoft x64 ABI and the
System V AMD64 ABI for the integer and pointer types used by wgpu-native.
purego applies the platform calling convention when those structs are passed
or returned by value.
"""

from __future__ import annotations

import json
import re
import struct
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
HEADER_DIR = ROOT / "third_party"
DLL = ROOT / "lib" / "wgpu_native.dll"
OUT = ROOT / "native"

KEYWORDS = {
    "break", "case", "chan", "const", "continue", "default", "defer", "else",
    "fallthrough", "for", "func", "go", "goto", "if", "import", "interface",
    "map", "package", "range", "return", "select", "struct", "switch", "type",
    "var",
}

PRIM = {
    "uint8_t": ("uint8", 1, 1),
    "uint16_t": ("uint16", 2, 2),
    "uint32_t": ("uint32", 4, 4),
    "uint64_t": ("uint64", 8, 8),
    "int8_t": ("int8", 1, 1),
    "int16_t": ("int16", 2, 2),
    "int32_t": ("int32", 4, 4),
    "int64_t": ("int64", 8, 8),
    "int": ("int32", 4, 4),
    "size_t": ("uintptr", 8, 8),
    "float": ("float32", 4, 4),
    "double": ("float64", 8, 8),
    "char": ("byte", 1, 1),
    "void": ("void", 0, 0),
}


def strip_comments(src: str) -> str:
    out = []
    i = 0
    n = len(src)
    while i < n:
        if src.startswith("/*", i):
            j = src.find("*/", i + 2)
            if j < 0:
                raise SystemExit("unterminated comment")
            out.append(" ")
            i = j + 2
        elif src.startswith("//", i):
            j = src.find("\n", i)
            i = n if j < 0 else j
        else:
            out.append(src[i])
            i += 1
    return "".join(out)


def strip_preprocessor(src: str) -> str:
    lines = src.split("\n")
    out = []
    i = 0
    while i < len(lines):
        if lines[i].lstrip().startswith("#"):
            while lines[i].rstrip().endswith("\\"):
                i += 1
            i += 1
            continue
        out.append(lines[i])
        i += 1
    return "\n".join(out)


def drop_extern_c(text: str) -> str:
    while True:
        match = re.search(r'extern\s+"C"\s*\{', text)
        if not match:
            return text
        depth = 0
        close = None
        for index in range(match.end() - 1, len(text)):
            if text[index] == "{":
                depth += 1
            elif text[index] == "}":
                depth -= 1
                if depth == 0:
                    close = index
                    break
        if close is None:
            raise SystemExit("unbalanced extern \"C\"")
        text = text[: match.start()] + text[match.end() : close] + text[close + 1 :]


def preprocess(path: Path) -> str:
    text = path.read_text(encoding="utf-8")
    text = strip_comments(text)
    text = strip_preprocessor(text)
    text = re.sub(
        r"\bWGPU_(?:STRUCTURE|ENUM|FUNCTION|OBJECT)_ATTRIBUTE\b",
        "",
        text,
    )
    text = re.sub(r"\bWGPU_NULLABLE\b", "", text)
    text = re.sub(r"\bWGPU_EXPORT\b", "", text)
    return drop_extern_c(text)


def split_statements(src: str) -> list[str]:
    stmts = []
    buf = []
    depth = 0
    for ch in src:
        if ch == "{":
            depth += 1
            buf.append(ch)
        elif ch == "}":
            depth -= 1
            buf.append(ch)
        elif ch == ";" and depth == 0:
            stmt = "".join(buf).strip()
            if stmt:
                stmts.append(stmt)
            buf = []
        else:
            buf.append(ch)
    tail = "".join(buf).strip()
    if tail:
        raise SystemExit(f"trailing tokens: {tail[:120]!r}")
    return stmts


def split_top(src: str, sep: str) -> list[str]:
    parts = []
    buf = []
    depth = 0
    for ch in src:
        if ch in "({":
            depth += 1
        elif ch in ")}":
            depth -= 1
        elif ch == sep and depth == 0:
            parts.append("".join(buf))
            buf = []
            continue
        buf.append(ch)
    tail = "".join(buf)
    if tail.strip():
        parts.append(tail)
    return parts


def go_ident(name: str) -> str:
    if not name:
        raise SystemExit("empty identifier")
    ident = name[0].upper() + name[1:]
    if ident in KEYWORDS:
        ident += "_"
    return ident


def c_to_go_type_name(name: str) -> str:
    if not name.startswith("WGPU"):
        raise SystemExit(f"unexpected C type name {name}")
    return go_ident(name[4:])


def eval_c(expr: str) -> int:
    expr = expr.strip()
    expr = expr.replace("U", "").replace("L", "")
    if not re.fullmatch(r"[0-9a-fA-FxX\s\|\(\)\<\>\+\-\*]+", expr):
        raise SystemExit(f"cannot evaluate {expr}")
    return int(eval(expr, {"__builtins__": {}}, {}))


def go_literal(expr: str) -> str:
    expr = " ".join(expr.split())
    if re.fullmatch(r"0[xX][0-9a-fA-F]+", expr):
        return hex(int(expr, 16))
    if re.fullmatch(r"\d+", expr):
        return expr
    value = eval_c(expr)
    if re.search(r"<<|\|", expr):
        return expr.replace(" ", "")
    return hex(value) if value > 9 else str(value)


def align_up(value: int, align: int) -> int:
    return (value + align - 1) & ~(align - 1)


def read_exports(path: Path) -> list[str]:
    data = path.read_bytes()
    e_lfanew = struct.unpack_from("<I", data, 0x3C)[0]
    coff = e_lfanew + 4
    _, nsec, _, _, _, optsize, _ = struct.unpack_from("<HHIIIHH", data, coff)
    opt = coff + 20
    export_rva, _ = struct.unpack_from("<II", data, opt + 112)
    sec_off = opt + optsize
    sections = []
    for i in range(nsec):
        sh = sec_off + i * 40
        vsize, va, rawsize, rawptr = struct.unpack_from("<IIII", data, sh + 8)
        sections.append((va, max(vsize, rawsize), rawptr))

    def rva_to_off(rva: int) -> int:
        for va, span, rawptr in sections:
            if va <= rva < va + span:
                return rawptr + (rva - va)
        raise SystemExit(f"bad rva {rva:#x}")

    off = rva_to_off(export_rva)
    ed = struct.unpack_from("<IIHHIIIIIII", data, off)
    nnames = ed[7]
    names_rva = ed[9]
    names = []
    for i in range(nnames):
        nrva = struct.unpack_from("<I", data, rva_to_off(names_rva) + i * 4)[0]
        end = data.index(b"\x00", rva_to_off(nrva))
        names.append(data[rva_to_off(nrva) : end].decode())
    return names


class Field:
    def __init__(self, name: str, go_type: str, size: int, align: int, offset: int = 0):
        self.name = name
        self.go_type = go_type
        self.size = size
        self.align = align
        self.offset = offset


class StructType:
    def __init__(self, cname: str, goname: str):
        self.cname = cname
        self.goname = goname
        self.fields: list[Field] = []
        self.size = 0
        self.align = 1
        self.kind = "struct"


class API:
    def __init__(self):
        self.order: list[str] = []
        self.types: dict[str, object] = {}
        self.consts: list[tuple[str, str, str, str]] = []
        self.functions: list[dict] = []
        self.enum_names: dict[str, str] = {}

    def add(self, cname: str, info: object) -> None:
        if cname in self.types:
            raise SystemExit(f"duplicate type {cname}")
        self.types[cname] = info
        self.order.append(cname)


def parse_union(body: str, api: API) -> tuple[str, int, int, str]:
    members = []
    for raw in split_top(body, ";"):
        raw = raw.strip()
        if not raw:
            continue
        members.append(parse_decl(raw, api))
    if not members:
        raise SystemExit("empty union")
    align = max(item[3] for item in members)
    size = align_up(max(item[2] for item in members), align)
    best = max(members, key=lambda item: (item[2], item[3]))
    note = ", ".join(item[0] for item in members)
    return best[1], size, align, note


def parse_decl(decl: str, api: API) -> tuple[str, str, int, int]:
    decl = " ".join(decl.replace("*", " * ").split())
    match = re.fullmatch(r"(?P<type>.+?) (?P<name>[A-Za-z_][A-Za-z0-9_]*)", decl.strip())
    if not match:
        raise SystemExit(f"bad declaration {decl!r}")
    c_type = match.group("type").strip()
    name = go_ident(match.group("name"))
    go_type, size, align = resolve_value_type(c_type, api)
    return name, go_type, size, align


def resolve_value_type(c_type: str, api: API) -> tuple[str, int, int]:
    text = " ".join(c_type.replace("*", " * ").split())
    if "*" in text:
        return "uintptr", 8, 8
    text = text.replace("const", "").replace("struct", "")
    text = " ".join(text.split())
    if text in PRIM:
        go, size, align = PRIM[text]
        if go == "void":
            raise SystemExit(f"void value in {c_type}")
        return go, size, align
    info = api.types.get(text)
    if info is None:
        raise SystemExit(f"unknown type {c_type!r}")
    if isinstance(info, StructType):
        if info.size == 0:
            raise SystemExit(f"struct {text} used before its size is known")
        return info.goname, info.size, info.align
    kind, goname, size, align = info
    if kind == "fnptr":
        return goname, 8, 8
    return goname, size, align


def parse_struct_body(body: str, api: API) -> list[Field]:
    fields = []
    for raw in split_top(body, ";"):
        raw = raw.strip()
        if not raw:
            continue
        union = re.fullmatch(
            r"union\s*\{(?P<body>.*)\}\s*(?P<name>[A-Za-z_][A-Za-z0-9_]*)",
            raw,
            flags=re.S,
        )
        if union:
            go_type, size, align, note = parse_union(union.group("body"), api)
            field = Field(go_ident(union.group("name")), go_type, size, align)
            field.note = f"C union ({note}); layout is the largest member"
            fields.append(field)
            continue
        name, go_type, size, align = parse_decl(raw, api)
        fields.append(Field(name, go_type, size, align))
    offset = 0
    align = 1
    for field in fields:
        offset = align_up(offset, field.align)
        field.offset = offset
        offset += field.size
        align = max(align, field.align)
    total = align_up(offset, align) if fields else 0
    return fields, total, align


def record_alias(api: API, base: str, name: str) -> None:
    if base in PRIM:
        go, size, align = PRIM[base]
        api.add(name, ("alias", c_to_go_type_name(name), size, align))
        return
    info = api.types.get(base)
    if info is None:
        raise SystemExit(f"alias {name} of unknown {base}")
    if isinstance(info, StructType):
        raise SystemExit(f"unexpected struct alias {name}")
    _, _, size, align = info
    api.add(name, ("alias", c_to_go_type_name(name), size, align))


def parse_enum(api: API, stmt: str) -> None:
    match = re.fullmatch(
        r"typedef\s+enum(?:\s+(?P<tag>[A-Za-z_][A-Za-z0-9_]*))?\s*\{(?P<body>.*)\}\s*(?P<name>[A-Za-z_][A-Za-z0-9_]*)",
        stmt,
        flags=re.S,
    )
    if not match:
        raise SystemExit(f"bad enum {stmt[:80]!r}")
    cname = match.group("name")
    goname = c_to_go_type_name(cname)
    api.add(cname, ("enum", goname, 4, 4))
    api.enum_names[cname] = goname
    value = None
    for part in split_top(match.group("body"), ","):
        part = " ".join(part.split())
        if not part:
            continue
        if "=" in part:
            ident, expr = part.split("=", 1)
            ident = ident.strip()
            expr = expr.strip()
            value = eval_c(expr)
            literal = go_literal(expr)
        else:
            ident = part.strip()
            value = 0 if value is None else value + 1
            literal = str(value)
        prefix, _, suffix = ident.partition("_")
        if prefix in api.enum_names or prefix in api.types:
            target = api.enum_names.get(prefix)
            if target is None:
                info = api.types.get(prefix)
                target = info[1] if info else goname
            const_go = target + go_ident(suffix) if suffix else target
            const_type = target
        else:
            const_go = goname + go_ident(suffix) if suffix else goname
            const_type = goname
        api.consts.append((const_go, const_type, literal, cname))


def parse_fnptr(api: API, stmt: str) -> None:
    match = re.fullmatch(
        r"typedef\s+.+\(\s*\*\s*(?P<name>WGPU[A-Za-z0-9_]+)\s*\)\s*\(.*\)",
        stmt,
        flags=re.S,
    )
    if not match:
        raise SystemExit(f"bad function pointer {stmt[:80]!r}")
    cname = match.group("name")
    if cname.startswith("WGPUProc") and cname != "WGPUProc":
        return
    api.add(cname, ("fnptr", c_to_go_type_name(cname), 8, 8))


def parse_struct(api: API, stmt: str) -> None:
    match = re.fullmatch(
        r"typedef\s+struct(?:\s+(?P<tag>[A-Za-z_][A-Za-z0-9_]*))?\s*\{(?P<body>.*)\}\s*(?P<name>[A-Za-z_][A-Za-z0-9_]*)",
        stmt,
        flags=re.S,
    )
    if not match:
        raise SystemExit(f"bad struct {stmt[:80]!r}")
    cname = match.group("name")
    info = StructType(cname, c_to_go_type_name(cname))
    api.add(cname, info)
    fields, size, align = parse_struct_body(match.group("body"), api)
    info.fields = fields
    info.size = size
    info.align = align


def normalize_param_type(c_type: str) -> str:
    return " ".join(c_type.replace("*", " * ").split())


def go_param_type(c_type: str, api: API) -> str:
    text = normalize_param_type(c_type)
    stars = text.count("*")
    base = text.replace("*", " ").replace("const", " ").replace("struct", " ")
    base = " ".join(base.split())
    if stars == 0:
        if base in ("void", ""):
            raise SystemExit(f"empty param type {c_type}")
        go, _, _ = resolve_value_type(base, api)
        return go
    if stars != 1:
        raise SystemExit(f"unsupported pointer depth in {c_type}")
    if base in ("void", "char", "uint8_t"):
        return "unsafe.Pointer"
    if base in PRIM:
        return "*" + PRIM[base][0]
    info = api.types.get(base)
    if info is None:
        raise SystemExit(f"unknown param type {c_type}")
    if isinstance(info, StructType):
        return "*" + info.goname
    return "*" + info[1]


def go_return_type(c_type: str, api: API) -> str | None:
    text = normalize_param_type(c_type)
    if text.replace(" ", "") in ("void", ""):
        return None
    stars = text.count("*")
    base = text.replace("*", " ").replace("const", " ").replace("struct", " ")
    base = " ".join(base.split())
    if stars:
        if base in ("void", "char", "uint8_t"):
            return "unsafe.Pointer"
        raise SystemExit(f"unsupported return pointer {c_type}")
    go, _, _ = resolve_value_type(base, api)
    return go


def parse_function(api: API, stmt: str, exports: set[str]) -> None:
    compact = " ".join(stmt.replace("*", " * ").split())
    match = re.fullmatch(
        r"(?P<ret>.+)\s+(?P<name>wgpu[A-Za-z0-9_]+)\s*\((?P<args>.*)\)",
        compact,
    )
    if not match:
        raise SystemExit(f"bad function {stmt[:120]!r}")
    cname = match.group("name")
    if cname not in exports:
        return
    ret = go_return_type(match.group("ret"), api)
    args = []
    raw_args = " ".join(match.group("args").split())
    if raw_args and raw_args != "void":
        for part in split_top(match.group("args"), ","):
            part = " ".join(part.replace("*", " * ").split())
            found = re.fullmatch(r"(?P<type>.+?) (?P<name>[A-Za-z_][A-Za-z0-9_]*)", part.strip())
            if not found:
                raise SystemExit(f"bad parameter {part!r} in {cname}")
            pname = found.group("name")
            if pname in KEYWORDS:
                pname += "_"
            args.append((pname, go_param_type(found.group("type"), api)))
    api.functions.append(
        {
            "c": cname,
            "go": go_ident(cname[4:]),
            "ret": ret,
            "args": args,
        }
    )


def parse_static_const(api: API, stmt: str) -> None:
    match = re.fullmatch(
        r"static\s+const\s+(?P<type>WGPU[A-Za-z0-9_]+)\s+(?P<name>WGPU[A-Za-z0-9_]+)\s*=\s*(?P<expr>.+)",
        " ".join(stmt.split()),
    )
    if not match:
        raise SystemExit(f"bad const {stmt!r}")
    info = api.types[match.group("type")]
    goname_type = info[1]
    ident = match.group("name")
    prefix, _, suffix = ident.partition("_")
    const_go = goname_type + (go_ident(suffix) if suffix else "")
    api.consts.append((const_go, goname_type, go_literal(match.group("expr")), match.group("type")))


def classify(api: API, stmt: str, exports: set[str]) -> None:
    compact = " ".join(stmt.split())
    if compact.startswith("typedef enum"):
        parse_enum(api, compact)
    elif compact.startswith("typedef struct") and "{" in compact:
        parse_struct(api, compact)
    elif re.match(r"typedef\s+struct\s+\w+\s*\*\s*WGPU", compact):
        name = re.fullmatch(r"typedef\s+struct\s+\w+\s*\*\s*(WGPU[A-Za-z0-9_]+)", compact).group(1)
        api.add(name, ("handle", c_to_go_type_name(name), 8, 8))
    elif re.match(r"typedef\s+.+\(\s*\*", compact):
        parse_fnptr(api, compact)
    elif compact.startswith("typedef "):
        match = re.fullmatch(r"typedef\s+([A-Za-z_][A-Za-z0-9_]*)\s+(WGPU[A-Za-z0-9_]+)", compact)
        if not match:
            raise SystemExit(f"bad typedef {compact}")
        record_alias(api, match.group(1), match.group(2))
    elif compact.startswith("static const "):
        parse_static_const(api, compact)
    elif compact.startswith("struct "):
        return
    elif re.search(r"\bwgpu[A-Z]", compact):
        parse_function(api, compact, exports)
    else:
        raise SystemExit(f"unrecognized statement: {compact[:160]}")


def topo_structs(api: API) -> list[StructType]:
    structs = [info for info in api.types.values() if isinstance(info, StructType)]
    by_go = {info.goname: info for info in structs}
    visiting = set()
    visited = set()
    ordered = []

    def visit(info: StructType) -> None:
        if info.goname in visited:
            return
        if info.goname in visiting:
            raise SystemExit(f"struct cycle at {info.cname}")
        visiting.add(info.goname)
        for field in info.fields:
            dep = by_go.get(field.go_type)
            if dep is not None:
                visit(dep)
        visiting.remove(info.goname)
        visited.add(info.goname)
        ordered.append(info)

    for info in structs:
        visit(info)
    return ordered


def emit_types(api: API) -> str:
    lines = [
        "// Code generated by gen/generate.py. DO NOT EDIT.",
        "",
        "//go:build windows && amd64",
        "",
        "package native",
        "",
        'import "fmt"',
        "",
        "// Integer and pointer layouts match wgpu-native on amd64.",
        "// Pointer fields are uintptr so a C address is not treated as a Go pointer.",
        "",
    ]
    consts_by_type: dict[str, list[tuple[str, str]]] = {}
    seen_const = set()
    for go_name, go_type, literal, _ in api.consts:
        if go_name in seen_const:
            raise SystemExit(f"duplicate const {go_name}")
        seen_const.add(go_name)
        consts_by_type.setdefault(go_type, []).append((go_name, literal))

    emitted_const_types = set()

    def emit_consts(goname: str) -> None:
        items = consts_by_type.get(goname)
        if not items or goname in emitted_const_types:
            return
        emitted_const_types.add(goname)
        lines.append("const (")
        for name, literal in items:
            lines.append(f"\t{name} {goname} = {literal}")
        lines.append(")")
        lines.append("")
        lines.append(f"func (v {goname}) String() string {{")
        lines.append("\tswitch v {")
        seen_values = set()
        for name, literal in items:
            value = eval_c(literal)
            if value in seen_values:
                continue
            seen_values.add(value)
            label = name[len(goname) :] or name
            lines.append(f"\tcase {name}:")
            lines.append(f"\t\treturn {json.dumps(label)}")
        lines.append("\tdefault:")
        lines.append(f"\t\treturn fmt.Sprintf({json.dumps(goname + '(%d)')}, v)")
        lines.append("\t}")
        lines.append("}")
        lines.append("")

    for cname in api.order:
        info = api.types[cname]
        if isinstance(info, StructType):
            continue
        kind, goname, _, _ = info
        if kind == "enum":
            lines.append(f"// {goname} is the C enum {cname}.")
            lines.append(f"type {goname} uint32")
            lines.append("")
            emit_consts(goname)
        elif kind == "alias":
            base = "uint64" if info[2] == 8 and info[3] == 8 and goname != "Bool" else None
            # Recover the Go primitive from size/align. Bool is a uint32 alias.
            primitive = {
                (1, 1): "uint8",
                (2, 2): "uint16",
                (4, 4): "uint32",
                (8, 8): "uint64",
            }.get((info[2], info[3]), "uintptr")
            if goname == "Bool":
                primitive = "uint32"
            if cname in ("WGPUFlags",) or (primitive == "uint64" and goname not in ("SubmissionIndex",)):
                # Flags and flag sets are uint64. SubmissionIndex is also uint64
                # but is not a bit set; both still use the same integer width.
                pass
            lines.append(f"// {goname} is the C typedef {cname}.")
            lines.append(f"type {goname} {primitive}")
            lines.append("")
            emit_consts(goname)
        elif kind == "handle":
            lines.append(f"// {goname} is an opaque {cname} handle.")
            lines.append(f"type {goname} uintptr")
            lines.append("")
        elif kind == "fnptr":
            lines.append(f"// {goname} is the C function pointer {cname}.")
            lines.append(f"type {goname} uintptr")
            lines.append("")
        else:
            raise SystemExit(kind)
        _ = base

    for info in topo_structs(api):
        lines.append(f"// {info.goname} is the C struct {info.cname}.")
        lines.append(f"type {info.goname} struct {{")
        for field in info.fields:
            note = getattr(field, "note", "")
            if note:
                lines.append(f"\t// {note}")
            lines.append(f"\t{field.name} {field.go_type}")
        lines.append("}")
        lines.append("")

    missing = set(consts_by_type) - emitted_const_types
    if missing:
        raise SystemExit(f"consts without types: {missing}")
    return "\n".join(lines)


def emit_funcs(api: API) -> str:
    lines = [
        "// Code generated by gen/generate.py. DO NOT EDIT.",
        "",
        "//go:build windows && amd64",
        "",
        "package native",
        "",
        "import (",
        '\t"unsafe"',
        "",
        '\t"github.com/ebitengine/purego"',
        ")",
        "",
        "// Function variables are filled by register. A nil variable means Load has not",
        "// been called. Struct arguments follow the platform C ABI via purego.",
        "",
    ]
    for fn in api.functions:
        args = ", ".join(f"{name} {typ}" for name, typ in fn["args"])
        ret = "" if fn["ret"] is None else " " + fn["ret"]
        lines.append(f"// {fn['go']} is {fn['c']}.")
        lines.append(f"var {fn['go']} func({args}){ret}")
        lines.append("")
    lines.append("func register(lib uintptr) {")
    for fn in api.functions:
        lines.append(f'\tpurego.RegisterLibFunc(&{fn["go"]}, lib, "{fn["c"]}")')
    lines.append("}")
    lines.append("")
    lines.append("var symbols = []string{")
    for fn in api.functions:
        lines.append(f'\t"{fn["c"]}",')
    lines.append("}")
    lines.append("")
    return "\n".join(lines)


def emit_layout_test(api: API) -> str:
    lines = [
        "// Code generated by gen/generate.py. DO NOT EDIT.",
        "",
        "//go:build windows && amd64",
        "",
        "package native",
        "",
        "import (",
        '\t"testing"',
        '\t"unsafe"',
        ")",
        "",
        "func TestCLayout(t *testing.T) {",
    ]
    for info in topo_structs(api):
        lines.append(f"\tif got, want := unsafe.Sizeof({info.goname}{{}}), uintptr({info.size}); got != want {{")
        lines.append(f'\t\tt.Errorf("{info.goname} size = %d, want %d", got, want)')
        lines.append("\t}")
        lines.append(f"\tif got, want := unsafe.Alignof({info.goname}{{}}), uintptr({info.align}); got != want {{")
        lines.append(f'\t\tt.Errorf("{info.goname} align = %d, want %d", got, want)')
        lines.append("\t}")
        for field in info.fields:
            lines.append(
                f"\tif got, want := unsafe.Offsetof({info.goname}{{}}.{field.name}), uintptr({field.offset}); got != want {{"
            )
            lines.append(
                f'\t\tt.Errorf("{info.goname}.{field.name} offset = %d, want %d", got, want)'
            )
            lines.append("\t}")
    lines.append("}")
    lines.append("")
    return "\n".join(lines)


def main() -> None:
    exports = read_exports(DLL)
    export_set = set(exports)
    src = preprocess(HEADER_DIR / "webgpu.h") + "\n" + preprocess(HEADER_DIR / "wgpu.h")
    api = API()
    for stmt in split_statements(src):
        classify(api, stmt, export_set)
    bound = {fn["c"] for fn in api.functions}
    missing = sorted(export_set - bound)
    if missing:
        raise SystemExit("DLL exports not bound:\n" + "\n".join(missing))
    OUT.mkdir(exist_ok=True)
    (OUT / "types.go").write_text(emit_types(api), encoding="utf-8")
    (OUT / "funcs.go").write_text(emit_funcs(api), encoding="utf-8")
    (OUT / "layout_test.go").write_text(emit_layout_test(api), encoding="utf-8")
    print(f"types {len(api.types)} funcs {len(api.functions)} consts {len(api.consts)}")


if __name__ == "__main__":
    main()
