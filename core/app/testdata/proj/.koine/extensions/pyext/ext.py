import sys, json
def send(**o): sys.stdout.write(json.dumps(dict(jsonrpc="2.0", **o)) + "\n"); sys.stdout.flush()
for line in sys.stdin:
    m = json.loads(line)
    meth, id_ = m.get("method"), m.get("id")
    if meth == "initialize":
        send(method="register_tool", params={"name": "py_add", "description": "Add two ints (python ext)",
             "schema": {"type": "object", "properties": {"a": {"type": "integer"}, "b": {"type": "integer"}}, "required": ["a", "b"]}})
        send(id=id_, result={"events": ["input"]})
    elif meth == "tool.execute":
        a = m["params"]["args"]; send(id=id_, result={"content": str(a["a"] + a["b"])})
    elif meth == "event":
        t = m["params"]["payload"].get("text", "")
        send(id=id_, result={"text": t.replace("colour", "color")} if "colour" in t else {})
    elif meth == "shutdown":
        break
