-- For a host database created before the project was renamed from ImKit to
-- Talkwire: renames its tables and indexes to the names in talkwire.sql and
-- talkwire.prisma, keeping the data. Run it once, before deploying a host on
-- the renamed SDK. It does nothing for a table that is already renamed or was
-- never created.
--
-- In a Prisma schema, also rename the model ImkitCallRecord to
-- TalkwireCallRecord (copy talkwire.prisma again); the client property becomes
-- prisma.talkwireCallRecord.

BEGIN;

ALTER TABLE IF EXISTS "imkit_profiles" RENAME TO "talkwire_profiles";
ALTER INDEX IF EXISTS "imkit_profiles_pkey" RENAME TO "talkwire_profiles_pkey";
ALTER INDEX IF EXISTS "imkit_profiles_uid_key" RENAME TO "talkwire_profiles_uid_key";
ALTER INDEX IF EXISTS "imkit_profiles_handle_key_key" RENAME TO "talkwire_profiles_handle_key_key";

ALTER TABLE IF EXISTS "imkit_contacts" RENAME TO "talkwire_contacts";
ALTER INDEX IF EXISTS "imkit_contacts_pkey" RENAME TO "talkwire_contacts_pkey";
ALTER INDEX IF EXISTS "imkit_contacts_pair_key_key" RENAME TO "talkwire_contacts_pair_key_key";
ALTER INDEX IF EXISTS "imkit_contacts_requester_idx" RENAME TO "talkwire_contacts_requester_idx";
ALTER INDEX IF EXISTS "imkit_contacts_recipient_idx" RENAME TO "talkwire_contacts_recipient_idx";

ALTER TABLE IF EXISTS "imkit_admin_audit" RENAME TO "talkwire_admin_audit";
ALTER INDEX IF EXISTS "imkit_admin_audit_pkey" RENAME TO "talkwire_admin_audit_pkey";
ALTER INDEX IF EXISTS "imkit_admin_audit_idempotency_key_key" RENAME TO "talkwire_admin_audit_idempotency_key_key";
ALTER INDEX IF EXISTS "imkit_admin_audit_created_idx" RENAME TO "talkwire_admin_audit_created_idx";

ALTER TABLE IF EXISTS "imkit_call_records" RENAME TO "talkwire_call_records";
ALTER INDEX IF EXISTS "imkit_call_records_pkey" RENAME TO "talkwire_call_records_pkey";
ALTER INDEX IF EXISTS "imkit_call_records_created_idx" RENAME TO "talkwire_call_records_created_idx";
ALTER INDEX IF EXISTS "imkit_call_records_caller_idx" RENAME TO "talkwire_call_records_caller_idx";
ALTER INDEX IF EXISTS "imkit_call_records_callee_idx" RENAME TO "talkwire_call_records_callee_idx";

COMMIT;
