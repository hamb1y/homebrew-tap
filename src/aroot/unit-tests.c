/* Exercise production parsing, token-file authentication and audit using real files.
 * Socket integration remains in tests.py and is never replaced by these tests. */
#define AROOT_TESTING 1
#define main aroot_program_main
#include "aroot.c"
#undef main
#include <assert.h>

int main(void) {
    if (geteuid() == 0) { fputs("unit tests require nonroot\n", stderr); return 1; }
    owner_uid = geteuid();
    char temporary[] = "/tmp/aroot-unit-XXXXXX";
    char *dir = mkdtemp(temporary);
    assert(dir);
    policy_path = dir;
    char policy[PATH_MAX], other[PATH_MAX], log[PATH_MAX];
    assert(snprintf(policy, sizeof policy, "%s/%lu", dir, (unsigned long)owner_uid) > 0);
    assert(snprintf(other, sizeof other, "%s/link", dir) > 0);
    assert(snprintf(log, sizeof log, "%s/audit", dir) > 0);
    unsigned char token[TOKEN_SIZE], wrong[TOKEN_SIZE];
    assert(getrandom(token, sizeof token, 0) == sizeof token);
    memcpy(wrong, token, sizeof wrong); wrong[0] ^= 1;
    int fd = open(policy, O_WRONLY | O_CREAT | O_EXCL, 0600);
    assert(fd >= 0 && full_write(fd, token, sizeof token) == 0 && close(fd) == 0);
    assert(token_matches(owner_uid, token));
    assert(!token_matches(owner_uid, wrong));
    assert(!token_matches(owner_uid + 1, token));
    assert(chmod(policy, 0644) == 0);
    assert(!token_matches(owner_uid, token));
    assert(chmod(policy, 0600) == 0);
    assert(link(policy, other) == 0);
    assert(!token_matches(owner_uid, token));
    assert(unlink(other) == 0);
    assert(token_matches(owner_uid, token));
    assert(rename(policy, other) == 0 && symlink(other, policy) == 0);
    assert(!token_matches(owner_uid, token));
    assert(unlink(policy) == 0 && rename(other, policy) == 0);
    assert(chmod(dir, 0755) == 0);
    assert(!token_matches(owner_uid, token));
    assert(chmod(dir, 0700) == 0);
    fd = open(policy, O_WRONLY | O_TRUNC);
    assert(fd >= 0 && full_write(fd, token, sizeof token - 1) == 0 && close(fd) == 0);
    assert(!token_matches(owner_uid, token));

    char payload[] = "/bin/echo\0a b\0$(literal)\0";
    struct request req = {.magic = htonl(MAGIC), .version = htonl(1), .argc = htonl(3), .bytes = htonl(sizeof payload - 1)};
    char *args[MAX_ARGS + 1];
    assert(decode_args(&req, payload, args) == 0);
    assert(!strcmp(args[0], "/bin/echo") && !strcmp(args[1], "a b") && !strcmp(args[2], "$(literal)") && args[3] == NULL);
    req.magic = 0; assert(decode_args(&req, payload, args) == -1); req.magic = htonl(MAGIC);
    req.version = htonl(2); assert(decode_args(&req, payload, args) == -1); req.version = htonl(1);
    req.argc = htonl(129); assert(decode_args(&req, payload, args) == -1);
    req.argc = htonl(0); assert(decode_args(&req, payload, args) == -1);
    req.argc = htonl(2); assert(decode_args(&req, payload, args) == -1);
    req.argc = htonl(3); req.bytes = htonl(sizeof payload - 2); assert(decode_args(&req, payload, args) == -1);
    char empty[] = "\0"; req.argc = htonl(1); req.bytes = htonl(1); assert(decode_args(&req, empty, args) == -1);
    char too_long[MAX_ARG + 2]; memset(too_long, 'x', sizeof too_long); too_long[sizeof too_long - 1] = 0;
    req.bytes = htonl(sizeof too_long); assert(decode_args(&req, too_long, args) == -1);
    uid_t parsed;
    assert(parse_uid("1000", &parsed) == 0 && parsed == 1000);
    assert(parse_uid("-1", &parsed) == -1 && parse_uid("+1", &parsed) == -1 && parse_uid("4294967295", &parsed) == -1 && parse_uid("x", &parsed) == -1);
    char name[NAME_MAX + 1];
    assert(token_parent("/tmp/aroot-token", owner_uid, name) == -1);
    assert(token_parent("relative", owner_uid, name) == -1);
    assert(token_parent("/home/../bad", owner_uid, name) == -1);

    /* Real fork/exec checks remain usable even where socket syscalls are denied. */
    for (int absolute = 0; absolute < 2; absolute++) {
        pid_t child = fork(); assert(child >= 0);
        if (child == 0) {
            char *command[] = {absolute ? "/bin/true" : "true", NULL};
            exec_command(command); _exit(125);
        }
        int status; assert(waitpid(child, &status, 0) == child && WIFEXITED(status) && WEXITSTATUS(status) == 0);
    }
    fd = open(other, O_WRONLY | O_CREAT | O_EXCL, 0700);
    assert(fd >= 0 && full_write(fd, "exit 0\n", 7) == 0 && close(fd) == 0);
    pid_t text_child = fork(); assert(text_child >= 0);
    if (text_child == 0) { char *command[] = {other, NULL}; exec_command(command); _exit(errno == ENOEXEC ? 42 : 125); }
    int text_status; assert(waitpid(text_child, &text_status, 0) == text_child && WIFEXITED(text_status) && WEXITSTATUS(text_status) == 42);
    assert(unlink(other) == 0);

    char escaped[AUDIT_EXE_JSON];
    assert(!executable_json(NULL, escaped) && !strcmp(escaped, "null"));
    assert(!executable_json("quote\"slash\\newline\nbyte\xff", escaped));
    assert(!strcmp(escaped, "\"quote\\\"slash\\\\newline\\u000abyte\\u00ff\""));
    char long_identity[AUDIT_EXE_BYTES + 5];
    memset(long_identity, 'x', sizeof long_identity); long_identity[sizeof long_identity - 1] = 0;
    assert(executable_json(long_identity, escaped) && strlen(escaped) == AUDIT_EXE_BYTES + 2);
    char escaped_identity[AUDIT_EXE_BYTES + 1];
    memset(escaped_identity, 255, AUDIT_EXE_BYTES); escaped_identity[AUDIT_EXE_BYTES] = 0;
    assert(!executable_json(escaped_identity, escaped) && strlen(escaped) == AUDIT_EXE_BYTES * 6 + 2);
    resolve_executable("true");
    assert(audit_resolved[0] == '/');
    resolve_executable("usr/bin/true");
    assert(audit_resolved[0] == '/');
    resolve_executable("/definitely/not/an/aroot/program");
    assert(audit_resolved[0] == 0);
    audit_requested = "quote\"slash\\newline\nbyte\xff";
    resolve_executable("/bin/true");

    audit_path = log; audit_fd = open_audit();
    assert(audit_fd >= 0 && audit("unit", owner_uid, getpid(), 0) == 0);
    int reader = open(log, O_RDONLY); assert(reader >= 0);
    char record[AUDIT_EXE_JSON * 2 + 512];
    ssize_t record_bytes = read(reader, record, sizeof record - 1); assert(record_bytes > 0);
    record[record_bytes] = 0; close(reader);
    assert(strstr(record, "\"requested_executable\":\"quote\\\"slash\\\\newline\\u000abyte\\u00ff\""));
    assert(strstr(record, "\"resolved_executable\":\"/") && strstr(record, "\"requested_executable_truncated\":false"));
    audit_requested = NULL; audit_resolved[0] = 0;
    assert(chmod(log, 0400) == 0 && audit("unit-deny", owner_uid, getpid(), 0) == -1);
    close(audit_fd);
    assert(open_audit() == -1);
    assert(chmod(log, 0600) == 0);
    assert(link(log, other) == 0 && open_audit() == -1 && unlink(other) == 0);
    assert(unlink(log) == 0 && symlink(policy, log) == 0 && open_audit() == -1);
    assert(unlink(log) == 0 && unlink(policy) == 0 && rmdir(dir) == 0);
    erase(token, sizeof token); erase(wrong, sizeof wrong);
    puts("native parser/auth-file/exec/audit checks passed");
    return 0;
}
