BEGIN;

DROP TABLE IF EXISTS audit.security_events;
DROP TABLE IF EXISTS account.account_deletion_jobs;
DROP TABLE IF EXISTS account.consents;
DROP TABLE IF EXISTS account.household_members;
DROP TABLE IF EXISTS account.households;
DROP TABLE IF EXISTS account.product_memberships;
DROP TABLE IF EXISTS account.login_challenges;
DROP TABLE IF EXISTS account.products;
DROP TABLE IF EXISTS account.sessions;
DROP TABLE IF EXISTS account.identities;
DROP TABLE IF EXISTS account.parties;
DROP TABLE IF EXISTS account.users;

DROP SCHEMA IF EXISTS audit;
DROP SCHEMA IF EXISTS account;

COMMIT;
