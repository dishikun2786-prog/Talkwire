"""Generate the protocol v1 JSON Schemas from the field tables below.

Usage:  python -I tools/schema/gen.py            (writes contract/protocol/schema/)
        python -I tools/schema/gen.py --check    (exit 1 if the files are stale)

The tables mirror contract/protocol/spec/protocol-v1.md. Edit them here, never
the generated *.schema.json files.
"""
import json
import pathlib
import sys

ROOT = pathlib.Path(__file__).resolve().parents[2]
OUT = ROOT / "contract" / "protocol" / "schema"
DRAFT = "https://json-schema.org/draft/2020-12/schema"
BASE = "https://imkit.example/protocol/v1/"

S = {"type": "string"}
NS = {"type": "string", "minLength": 1}
I = {"type": "integer"}
POS = {"type": "integer", "minimum": 1}
B = {"type": "boolean"}
ANY = {}
MS = {"type": "integer", "minimum": 0, "description": "Unix milliseconds"}
UID = {"type": "string", "pattern": "^(uid_[A-Za-z0-9_-]{11}|usr.+)$"}
STRS = {"type": "array", "items": S}
UIDS = {"type": "array", "items": UID}
INTS = {"type": "array", "items": I}
PROVIDER = {"type": "string", "enum": ["fcm", "apns", "webpush"],
            "description": "push service of the token; absent means fcm"}
DEVICE = {"type": "string", "pattern": "^[A-Za-z0-9._:-]{0,64}$",
          "description": "the device the login comes from (multi-device); absent or empty: the device \"\", "
                         "one connection per user as with the reference server"}
ERRCODE = {"type": "string", "pattern": "^[a-z][a-z0-9_]*$",
           "description": "strict profile: stable machine-readable error code"}


def ref(name):
    return {"$ref": "#/$defs/" + name}


def obj(props, required=(), extra=True):
    """extra=False forbids undeclared properties."""
    o = {"type": "object", "properties": props}
    if required:
        o["required"] = list(required)
    if not extra:
        o["additionalProperties"] = False
    return o


# ---------------------------------------------------------------- client side
# action -> (properties, required). "id" and "action" are added to every one.
CLIENT = {
    "login": ({"token": NS, "device": DEVICE}, ["token"]),
    "sub": ({"topic": NS}, ["topic"]),
    "leave": ({"topic": NS}, ["topic"]),
    "pub": ({"topic": NS, "content": S, "contentType": S, "fileUrl": S, "fileName": S,
             "duration": I, "extra": ANY, "clientMsgId": S}, ["topic"]),
    "get": ({"topic": NS, "limit": I, "beforeSeq": I, "afterSeq": I}, ["topic"]),
    "del": ({"topic": NS, "seq": POS}, ["topic", "seq"]),
    "del_topic": ({"topic": NS}, ["topic"]),
    "note": ({"topic": NS, "what": {"enum": ["kp", "read"]}, "seq": I}, ["topic", "what"]),
    "pres": ({"topic": NS, "topics": STRS}, []),
    "create_grp": ({"name": NS, "members": UIDS}, ["name"]),
    "invite": ({"topic": NS, "user": UID}, ["topic", "user"]),
    "kick": ({"topic": NS, "user": UID}, ["topic", "user"]),
    "leave_grp": ({"topic": NS}, ["topic"]),
    "update_grp": ({"topic": NS, "name": NS}, ["topic", "name"]),
    "transfer_owner": ({"topic": NS, "user": UID}, ["topic", "user"]),
    "set_member_role": ({"topic": NS, "user": UID, "memberRole": {"enum": ["admin", "member"]}},
                        ["topic", "user", "memberRole"]),
    "disband_grp": ({"topic": NS}, ["topic"]),
    "get_groups": ({}, []),
    "get_members": ({"topic": NS}, ["topic"]),
    # strict profile, capability sync-summary (section 8.1).
    "sync_summary": ({}, []),
    "reg_token": ({"token": NS, "provider": PROVIDER}, ["token"]),
    "unreg_token": ({"token": NS, "provider": PROVIDER}, ["token"]),
    "ack_topic_cleared": ({}, ["id"]),
    "ack_group_event": ({"eventId": POS, "topic": S}, ["eventId"]),
    "forward_bundle": ({"reqId": NS, "sourceTopic": NS, "sourceMsgIds": {"type": "array", "items": POS, "minItems": 1},
                        "targetTopic": NS}, ["reqId", "sourceTopic", "sourceMsgIds", "targetTopic"]),
    "forward_broadcast": ({"reqId": NS, "sourceTopic": NS, "sourceMsgId": POS,
                           "targetTopics": {"type": "array", "items": NS, "minItems": 1}},
                          ["reqId", "sourceTopic", "sourceMsgId", "targetTopics"]),
    "get_group_ai_settings": ({"reqId": NS, "topic": NS}, ["reqId", "topic"]),
    "update_group_ai_settings": ({"reqId": NS, "topic": NS, "aiQaEnabled": B, "aiKbIds": STRS, "aiDocIds": STRS},
                                 ["reqId", "topic"]),
    "group_qr_token": ({"topic": NS, "refreshQr": B}, ["topic"]),
    "group_qr_resolve": ({"qrToken": NS}, ["qrToken"]),
    "group_join": ({"qrToken": NS, "joinMessage": S}, ["qrToken"]),
    "group_join_requests_list": ({"topic": NS}, ["topic"]),
    "group_join_review": ({"topic": NS, "joinRequestId": POS, "approve": B}, ["topic", "joinRequestId"]),
    "get_group_join_settings": ({"topic": NS}, ["topic"]),
    "update_group_join_settings": ({"topic": NS, "requireApproval": B}, ["topic"]),
    "e2e_publish_bundle": ({"reqId": NS, "e2eBundle": ref("e2eBundle")}, ["reqId", "e2eBundle"]),
    "e2e_fetch_bundle": ({"reqId": NS, "user": UID}, ["reqId", "user"]),
    "e2e_otpk_count": ({"reqId": NS}, ["reqId"]),
    "e2e_send": ({"topic": NS, "user": UID, "e2eKind": {"enum": ["msg", "sender_key"]}, "e2eMsgType": I,
                  "content": S}, ["topic", "e2eKind"]),
    "e2e_terminate": ({"topic": NS, "user": UID}, ["topic", "user"]),
    "e2e_clear": ({"topic": NS, "user": UID}, ["topic", "user"]),
    "e2e_group_clear": ({"topic": NS}, ["topic"]),
    "e2e_set_group_encrypted": ({"reqId": NS, "topic": NS}, ["reqId", "topic"]),
    "call_invite": ({"topic": UID, "callType": {"enum": ["voice", "video"]}, "clientCallId": S},
                    ["topic", "callType"]),
    "call_accept": ({"callSessionId": NS, "acceptAsVoice": B}, ["callSessionId"]),
    "call_reject": ({"callSessionId": NS}, ["callSessionId"]),
    "call_cancel": ({"callSessionId": NS}, ["callSessionId"]),
    "call_hangup": ({"callSessionId": NS}, ["callSessionId"]),
    "call_upgrade": ({"callSessionId": NS, "toType": {"const": "video"}}, ["callSessionId", "toType"]),
    "call_upgrade_accept": ({"callSessionId": NS}, ["callSessionId"]),
    "call_upgrade_reject": ({"callSessionId": NS}, ["callSessionId"]),
}

CLIENT_DEFS = {
    "e2eBundle": obj({
        "registrationId": I, "identityKey": NS, "signedPreKeyId": I, "signedPreKey": NS,
        "signedPreKeySignature": NS, "kyberPreKeyId": I, "kyberPreKey": NS, "kyberPreKeySignature": NS,
        "oneTimePreKeys": {"type": "array", "items": obj({"keyId": I, "preKey": NS}, ["keyId", "preKey"])},
    }, ["registrationId", "identityKey", "signedPreKeyId", "signedPreKey", "signedPreKeySignature",
        "kyberPreKeyId", "kyberPreKey", "kyberPreKeySignature"]),
}


def client_schema():
    branches = []
    for action, (props, req) in CLIENT.items():
        p = {"id": S, "action": {"const": action}}
        p.update(props)
        r = ["action"] + [x for x in req if x != "action"]
        branches.append({"if": {"properties": {"action": {"const": action}}, "required": ["action"]},
                         "then": obj(p, r)})
    return {
        "$schema": DRAFT,
        "$id": BASE + "client-frames.schema.json",
        "title": "ImKit protocol v1: frames a client sends",
        "description": "Generated by tools/schema/gen.py; do not edit. Unknown properties are allowed "
                       "(servers must ignore them). See contract/protocol/spec/protocol-v1.md.",
        "type": "object",
        "required": ["action"],
        "properties": {"id": S, "action": {"enum": list(CLIENT)}},
        "allOf": branches,
        "$defs": CLIENT_DEFS,
    }


# ---------------------------------------------------------------- server side
STORED = obj({
    "topic": NS, "seq": POS, "from": NS, "content": S, "contentType": S, "fileUrl": S, "fileName": S,
    "ts": MS, "duration": I, "extra": ANY, "senderKind": {"enum": ["user", "ai_assistant"]},
    "forwardFromMsgId": POS, "forwardFromTopic": NS,
}, ["topic", "seq", "from", "content", "contentType", "ts"], extra=False)

SERVER_DEFS = {
    "storedMessage": STORED,
    "groupInfo": obj({"topic": NS, "name": S, "owner": UID, "members": I, "encrypted": B},
                     ["topic", "name"], extra=False),
    "memberInfo": obj({"uid": UID, "name": S, "role": {"enum": ["owner", "admin", "member"]}, "joinedAt": MS},
                      ["uid"], extra=False),
    "readCursor": obj({"uid": UID, "seq": I}, ["uid", "seq"], extra=False),
    "syncTopic": obj({"topic": NS, "lastSeq": {"type": "integer", "minimum": 0}, "isGroup": B,
                      "clearVersion": I, "clearBeforeSeq": I, "peer": UID, "readSeq": POS, "readsAt": MS},
                     ["topic", "lastSeq", "isGroup"], extra=False),
    "presenceState": obj({"uid": UID, "online": B, "presRev": I, "lastSeenAt": MS}, ["uid"], extra=False),
    "joinRequest": obj({"id": POS, "requesterUid": UID, "requesterName": S, "message": S, "createdAt": MS},
                       ["id", "requesterUid", "createdAt"], extra=False),
    "forwardTargetResult": obj({"topic": S, "ok": B, "seq": POS, "reason": S}, ["topic", "ok"], extra=False),
}

E = {"id": S, "action": NS, "code": {"type": "integer", "minimum": 400}, "reason": S, "errCode": ERRCODE}
ERROR = obj(E, ["action", "code"], extra=False)

C200 = {"const": 200}
OKCODE = {"enum": [200, 201]}

# action -> list of alternative shapes (properties, required). "action" added automatically.
# Error replies (code >= 400) are handled once, generically.
SERVER_ACTION = {
    # id, protocol, capabilities and vapidKey: strict profile only.
    "login": [({"id": S, "code": C200, "uid": UID, "protocol": POS, "capabilities": STRS, "vapidKey": S},
               ["code", "uid"])],
    "error": [],
    "note": [],  # no success reply; only errors (e.g. non-member)
    "sub": [({"id": S, "code": C200, "topic": NS}, ["code", "topic"])],
    "leave": [({"id": S, "code": C200, "topic": NS}, ["code", "topic"])],
    "pub": [({"id": S, "code": C200, "topic": NS, "seq": POS, "ts": MS, "extra": ANY}, ["code", "topic", "seq", "ts"])],
    "data": [({"topic": NS, "from": NS, "seq": POS, "ts": MS, "content": S, "contentType": S, "fileUrl": S,
               "fileName": S, "duration": I, "extra": ANY, "senderKind": {"enum": ["user", "ai_assistant"]},
               "clientMsgId": S, "name": S, "forwardFromMsgId": POS, "forwardFromTopic": NS},
              ["topic", "from", "seq", "ts"])],
    "get": [({"id": S, "code": C200, "topic": NS, "messages": {"type": "array", "items": ref("storedMessage")},
              "clearVersion": I, "clearBeforeSeq": I,
              "readCursors": {"type": "array", "items": ref("readCursor")},
              # readSeq: strict profile only, the caller's own read position.
              "readSeq": POS}, ["code", "topic"])],
    "pres": [
        ({"topic": UID, "what": {"enum": ["on", "off"]}, "online": B, "presRev": I}, ["topic", "what"]),
        # id: strict profile only.
        ({"id": S, "code": C200, "what": {"const": "snapshot"}, "states": {"type": "array", "items": ref("presenceState")}},
         ["code", "what"]),
    ],
    "del": [({"id": S, "code": C200, "topic": NS, "seq": POS}, ["code", "topic", "seq"])],
    "recalled": [({"topic": NS, "seq": POS, "from": UID}, ["topic", "seq", "from"])],
    "del_topic": [({"id": S, "code": C200, "topic": NS, "clearVersion": I, "clearBeforeSeq": I,
                    "deletedMessages": I, "deletedMedia": I}, ["code", "topic"])],
    "topic_cleared": [({"id": S, "topic": NS, "from": UID, "clearVersion": POS, "clearBeforeSeq": I},
                       ["topic", "clearVersion"])],
    "info": [({"topic": NS, "from": UID, "what": NS, "seq": I}, ["topic", "from", "what"])],
    "create_grp": [({"id": S, "code": {"const": 201}, "topic": NS, "name": NS}, ["code", "topic", "name"])],
    "invite": [({"id": S, "code": C200, "topic": NS, "user": UID}, ["code", "topic", "user"])],
    "kick": [({"id": S, "code": C200, "topic": NS, "user": UID}, ["code", "topic", "user"])],
    "transfer_owner": [({"id": S, "code": C200, "topic": NS, "user": UID}, ["code", "topic", "user"])],
    "leave_grp": [({"id": S, "code": C200, "topic": NS}, ["code", "topic"])],
    "disband_grp": [({"id": S, "code": C200, "topic": NS}, ["code", "topic"])],
    "update_grp": [({"id": S, "code": C200, "topic": NS, "name": NS}, ["code", "topic", "name"])],
    "set_member_role": [({"id": S, "code": C200, "topic": NS, "user": UID,
                          "memberRole": {"enum": ["admin", "member"]}}, ["code", "topic", "user", "memberRole"])],
    "get_groups": [({"id": S, "code": C200, "groups": {"type": "array", "items": ref("groupInfo")}}, ["code"])],
    "get_members": [({"id": S, "code": C200, "topic": NS, "members": {"type": "array", "items": ref("memberInfo")}},
                     ["code", "topic"])],
    # strict profile, capability sync-summary (section 8.1).
    "sync_summary": [({"id": S, "code": C200, "topics": {"type": "array", "items": ref("syncTopic")}}, ["code"])],
    "grp_sync": [({"topic": NS, "name": S, "from": UID, "eventId": POS}, ["topic"])],
    "grp_disband": [({"topic": NS, "from": UID, "eventId": POS}, ["topic"])],
    "contact_sync": [({"topic": S, "what": {"enum": ["accepted", "removed"]}, "from": UID}, ["what", "from"])],
    # Host card updates (appendix B). topic is absent when sent to one user.
    "custom_update": [({"topic": NS, "what": {"type": "string", "pattern": "^[a-z][a-z0-9_.-]{0,63}$"},
                        "extra": {"not": {"type": "null"}}}, ["what", "extra"])],
    "red_packet_update": [({"topic": NS, "extra": ANY}, ["topic", "extra"])],
    # -- modules --
    "group_qr_token": [({"id": S, "code": C200, "topic": NS, "qrToken": NS, "expiresAt": MS},
                        ["code", "topic", "qrToken"])],
    "group_qr_resolve": [({"id": S, "code": C200, "topic": NS, "name": S, "memberCount": I, "alreadyMember": B,
                          "isFull": B, "requireApproval": B}, ["code", "topic"])],
    "group_join": [({"id": S, "code": C200, "topic": NS, "joinStatus": {"enum": ["joined", "pending_approval"]}},
                    ["code", "topic", "joinStatus"])],
    "group_join_requests_list": [({"id": S, "code": C200, "topic": NS,
                                   "joinRequests": {"type": "array", "items": ref("joinRequest")}},
                                  ["code", "topic"])],
    "group_join_review": [({"id": S, "code": C200, "topic": NS, "joinStatus": {"enum": ["approved", "rejected"]}},
                           ["code", "topic", "joinStatus"])],
    "get_group_join_settings": [({"id": S, "code": C200, "topic": NS, "requireApproval": B}, ["code", "topic"])],
    "update_group_join_settings": [({"id": S, "code": C200, "topic": NS, "requireApproval": B}, ["code", "topic"])],
    "group_join_request": [({"topic": NS, "name": S, "from": UID, "eventId": POS}, ["topic"])],
    "group_join_reviewed": [({"topic": NS, "name": S, "eventId": POS,
                              "joinStatus": {"enum": ["approved", "rejected"]}}, ["topic", "joinStatus"])],
    "e2e_send": [({"id": S, "code": C200, "topic": NS}, ["code", "topic"])],
    "e2e_deliver": [({"topic": NS, "from": UID, "e2eKind": NS, "e2eMsgType": I, "content": S, "ts": MS},
                     ["topic", "from", "e2eKind"])],
    "e2e_terminate": [({"id": S, "code": C200, "topic": NS}, ["code", "topic"])],
    "e2e_terminated": [({"topic": NS, "from": UID}, ["topic", "from"])],
    "e2e_clear": [({"id": S, "code": C200, "topic": NS}, ["code", "topic"])],
    "e2e_cleared": [({"topic": NS, "from": UID}, ["topic", "from"])],
    "e2e_group_clear": [({"id": S, "code": C200, "topic": NS}, ["code", "topic"])],
    "e2e_group_cleared": [({"topic": NS, "from": UID}, ["topic", "from"])],
    "e2e_set_group_encrypted": [({"topic": NS, "from": UID, "e2eGroupEncrypted": {"const": True},
                                  "reason": {"enum": ["owner_enabled", "member_removed", "member_left"]}},
                                 ["topic", "e2eGroupEncrypted"])],
    "call_invite": [({"id": S, "code": C200, "topic": UID, "callSessionId": NS}, ["code", "callSessionId"])],
    "call_accept": [({"id": S, "code": C200, "callSessionId": NS, "channelId": NS, "rtcToken": NS, "appId": S,
                      "videoQuality": S, "denoiseEnabled": B}, ["code", "callSessionId", "channelId", "rtcToken"])],
}
for a in ["call_reject", "call_cancel", "call_hangup", "call_upgrade", "call_upgrade_accept", "call_upgrade_reject"]:
    SERVER_ACTION[a] = [({"id": S, "code": C200, "callSessionId": NS}, ["code", "callSessionId"])]

R = {"reqId": S}
SERVER_TYPE = {
    "forward_bundle_result": ({"reqId": S, "ok": B, "insertedSeqs": {"type": "array", "items": POS}, "reason": S},
                              ["reqId", "ok"]),
    "forward_broadcast_result": ({"reqId": S, "results": {"type": "array", "items": ref("forwardTargetResult")}},
                                 ["reqId", "results"]),
    "group_ai_settings": ({"reqId": S, "topic": S, "aiQaEnabled": B, "aiKbIds": {"type": ["array", "null"], "items": S},
                           "aiDocIds": {"type": ["array", "null"], "items": S}, "aiDailyQuota": I, "aiCooldownSec": I},
                          ["topic", "aiQaEnabled", "aiKbIds", "aiDocIds", "aiDailyQuota", "aiCooldownSec"]),
    "update_group_ai_settings_result": ({"reqId": S, "ok": B, "reason": S}, ["reqId", "ok"]),
    "ai_typing": ({"topic": NS}, ["topic"]),
    "e2e_bundle": ({"reqId": S, "user": S, "found": B, "deviceId": I, "registrationId": I, "identityKey": S,
                    "signedPreKeyId": I, "signedPreKey": S, "signedPreKeySignature": S, "kyberPreKeyId": I,
                    "kyberPreKey": S, "kyberPreKeySignature": S, "oneTimePreKeyId": I, "oneTimePreKey": S},
                   ["user", "found"]),
    "e2e_publish_bundle_result": ({"reqId": S, "ok": B, "reason": S}, ["ok"]),
    "e2e_set_group_encrypted_result": ({"reqId": S, "ok": B, "reason": S}, ["ok"]),
    "e2e_otpk_count_result": ({"reqId": S, "count": I}, ["count"]),
    "call_ring": ({"callSessionId": NS, "callType": {"enum": ["voice", "video"]}, "callerName": S, "callerAvatar": S,
                   "ringTimeoutSeconds": I}, ["callSessionId", "callType"]),
    "call_answered": ({"callSessionId": NS, "channelId": NS, "rtcToken": NS, "appId": S, "videoQuality": S,
                       "denoiseEnabled": B}, ["callSessionId", "channelId", "rtcToken"]),
    "call_ended": ({"callSessionId": NS, "reason": S}, ["callSessionId"]),
    "call_upgrade_request": ({"callSessionId": NS, "toType": {"const": "video"}}, ["callSessionId", "toType"]),
    "call_upgraded": ({"callSessionId": NS, "videoQuality": S}, ["callSessionId"]),
    "call_upgrade_declined": ({"callSessionId": NS}, ["callSessionId"]),
}


def server_schema():
    action_branches = []
    for action, shapes in SERVER_ACTION.items():
        alts = []
        for props, req in shapes:
            p = {"action": {"const": action}}
            p.update(props)
            alts.append(obj(p, ["action"] + req, extra=False))
        alts.append(ERROR)
        action_branches.append({"if": {"properties": {"action": {"const": action}}, "required": ["action"]},
                                "then": {"oneOf": alts} if len(alts) > 1 else alts[0]})
    type_branches = []
    for t, (props, req) in SERVER_TYPE.items():
        p = {"type": {"const": t}}
        p.update(props)
        type_branches.append({"if": {"properties": {"type": {"const": t}}, "required": ["type"]},
                              "then": obj(p, ["type"] + req, extra=False)})
    return {
        "$schema": DRAFT,
        "$id": BASE + "server-frames.schema.json",
        "title": "ImKit protocol v1: frames a server sends",
        "description": "Generated by tools/schema/gen.py; do not edit. Undeclared properties are rejected so "
                       "that a server cannot emit fields the spec does not document. Clients must NOT validate "
                       "with this schema at run time: they ignore unknown fields. See "
                       "contract/protocol/spec/protocol-v1.md.",
        "type": "object",
        "anyOf": [
            {"required": ["action"], "not": {"required": ["type"]},
             "properties": {"action": {"enum": list(SERVER_ACTION)}}, "allOf": action_branches},
            {"required": ["type"], "not": {"required": ["action"]},
             "properties": {"type": {"enum": list(SERVER_TYPE)}}, "allOf": type_branches},
            # An error reply echoes the request's action verbatim, including an
            # unknown one ("unknown action: <name>"), so it is accepted for any action.
            ERROR,
        ],
        "$defs": SERVER_DEFS,
    }


def render(schema):
    return json.dumps(schema, ensure_ascii=False, indent=2) + "\n"


def main():
    files = {"client-frames.schema.json": render(client_schema()),
             "server-frames.schema.json": render(server_schema())}
    check = "--check" in sys.argv
    stale = []
    OUT.mkdir(parents=True, exist_ok=True)
    for name, text in files.items():
        path = OUT / name
        old = path.read_text(encoding="utf-8") if path.exists() else None
        if old != text:
            stale.append(name)
            if not check:
                path.write_text(text, encoding="utf-8", newline="\n")
    if check and stale:
        print("stale:", ", ".join(stale))
        sys.exit(1)
    print(("checked" if check else "wrote"), len(files), "schema file(s);", "changed:", stale or "none")


if __name__ == "__main__":
    main()
