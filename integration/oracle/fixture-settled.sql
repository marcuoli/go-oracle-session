-- Run only in the dedicated disposable qualification schema, never an
-- application schema. The harness verifies the exact per-run fixture first.
create table native_event (token varchar2(32) not null);

-- Commit dispatch evidence independently of the canceled statement. The
-- observer can then distinguish actual server work from an undispatched call.
create function native_wait(p_token varchar2) return varchar2
is
 pragma autonomous_transaction;
begin
 insert into native_event(token) values(p_token);
 commit;
 sys.dbms_lock.sleep(3);
 return 'completed';
end;
/
