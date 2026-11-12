-- Approvals on the SDK's flow kernel (be-protocol chapter 17; its tables come with the SDK's migrations): the policy's
-- approver, a position of mdm/org or one principal (at most one); the legal entity's time zone on the documents, so an
-- approval decides who holds the position on the legal entity's business date inside its transaction.
ALTER TABLE purchase_policy ADD COLUMN approver_position_id uuid;
ALTER TABLE purchase_policy ADD COLUMN approver_principal text;
ALTER TABLE purchase_policy ADD CONSTRAINT purchase_policy_one_approver
    CHECK (approver_position_id IS NULL OR approver_principal IS NULL);
ALTER TABLE purchase_order ADD COLUMN timezone text NOT NULL DEFAULT 'UTC';
ALTER TABLE requisition ADD COLUMN timezone text NOT NULL DEFAULT 'UTC';
