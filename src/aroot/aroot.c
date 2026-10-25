#define _GNU_SOURCE
#include <arpa/inet.h>
#include <errno.h>
#include <fcntl.h>
#include <grp.h>
#include <limits.h>
#include <poll.h>
#include <signal.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/file.h>
#include <sys/prctl.h>
#include <sys/random.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/un.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>
#include <pwd.h>

#define VERSION "1.0.0"
#define MAGIC 0x41525431U
#define MAX_ARGS 128U
#define MAX_PAYLOAD 65536U
#define MAX_ARG 4096U
#define TOKEN_SIZE 32
#define MAX_WORKERS 32
#define AUDIT_EXE_BYTES 512
#define AUDIT_EXE_JSON (AUDIT_EXE_BYTES * 6 + 3)
#define SOCKET_PATH "/run/aroot/control.sock"
#define POLICY_PATH "/etc/aroot/users"
#define AUDIT_PATH "/var/log/aroot/audit.jsonl"

struct request { uint32_t magic, version, argc, bytes; unsigned char token[TOKEN_SIZE]; };
struct reply { uint32_t magic, status; };
static const char *socket_path = SOCKET_PATH, *policy_path = POLICY_PATH, *audit_path = AUDIT_PATH;
static int audit_fd = -1;
static const char *audit_requested;
static char audit_resolved[PATH_MAX];
static const char *system_dirs[] = {"/usr/sbin", "/usr/bin", "/sbin", "/bin"};
static uid_t owner_uid;
static volatile sig_atomic_t stopping, child_changed, pending_signal;
static void erase(void *p, size_t n) { explicit_bzero(p, n); }
static void die(const char *s) { perror(s); exit(125); }
static int full_read(int fd, void *buf, size_t n) {
    unsigned char *p = buf;
    while (n) { ssize_t k = read(fd, p, n); if (k < 0 && errno == EINTR) continue; if (k <= 0) return -1; p += k; n -= (size_t)k; }
    return 0;
}
static int64_t monotonic_millis(void) {
    struct timespec ts;
    if (clock_gettime(CLOCK_MONOTONIC, &ts) < 0) return -1;
    return (int64_t)ts.tv_sec * 1000 + ts.tv_nsec / 1000000;
}
static int read_request_payload(int fd, void *buf, size_t n, int64_t deadline) {
    unsigned char *p = buf;
    while (n) {
        int64_t now = monotonic_millis();
        if (now < 0 || now >= deadline) return -1;
        struct pollfd ready = {.fd = fd, .events = POLLIN};
        int rc = poll(&ready, 1, (int)(deadline - now));
        if (rc < 0 && errno == EINTR) continue;
        if (rc <= 0) return -1;
        ssize_t got = recv(fd, p, n, MSG_DONTWAIT);
        if (got < 0 && (errno == EAGAIN || errno == EINTR)) continue;
        if (got <= 0) return -1;
        p += got; n -= (size_t)got;
    }
    return 0;
}
static int full_write(int fd, const void *buf, size_t n) {
    const unsigned char *p = buf;
    while (n) { ssize_t k = write(fd, p, n); if (k < 0 && errno == EINTR) continue; if (k <= 0) return -1; p += k; n -= (size_t)k; }
    return 0;
}
static int safe_file(int fd, mode_t mode) {
    struct stat st;
    return fstat(fd, &st) == 0 && S_ISREG(st.st_mode) && st.st_uid == owner_uid && st.st_nlink == 1 && (st.st_mode & 0777) == mode;
}
static int safe_dir(const char *path, mode_t mode) {
    struct stat st;
    return lstat(path, &st) == 0 && S_ISDIR(st.st_mode) && st.st_uid == owner_uid && (st.st_mode & 0777) == mode;
}
static int open_audit(void) {
    char parent[PATH_MAX];
    if (strlen(audit_path) >= sizeof parent) return -1;
    strcpy(parent, audit_path);
    char *slash = strrchr(parent, '/');
    if (!slash || slash == parent) return -1;
    *slash = '\0';
    if (!safe_dir(parent, 0700)) return -1;
    int fd = open(audit_path, O_WRONLY | O_APPEND | O_CREAT | O_NOFOLLOW | O_NONBLOCK | O_CLOEXEC, 0600);
    if (fd >= 0 && !safe_file(fd, 0600)) { close(fd); fd = -1; }
    return fd;
}
/* Bound source bytes, escape arbitrary Unix filename bytes, and signal truncation. */
static bool executable_json(const char *value, char output[AUDIT_EXE_JSON]) {
    if (!value) { strcpy(output, "null"); return false; }
    const char hex[] = "0123456789abcdef";
    size_t used = 0, at = 0; output[at++] = '"';
    while (value[used] && used < AUDIT_EXE_BYTES) {
        unsigned char c = (unsigned char)value[used++];
        if (c == '"' || c == '\\') { output[at++] = '\\'; output[at++] = (char)c; }
        else if (c < 32 || c >= 127) {
            output[at++] = '\\'; output[at++] = 'u'; output[at++] = '0'; output[at++] = '0';
            output[at++] = hex[c >> 4]; output[at++] = hex[c & 15];
        } else output[at++] = (char)c;
    }
    bool truncated = value[used] != 0;
    output[at++] = '"'; output[at] = 0; return truncated;
}
static int audit(const char *event, uid_t uid, pid_t pid, int status) {
    char line[AUDIT_EXE_JSON * 2 + 512], requested[AUDIT_EXE_JSON], resolved[AUDIT_EXE_JSON];
    bool requested_truncated = executable_json(audit_requested, requested);
    bool resolved_truncated = executable_json(audit_resolved[0] ? audit_resolved : NULL, resolved);
    struct timespec ts;
    if (clock_gettime(CLOCK_REALTIME, &ts) < 0) return -1;
    int n = snprintf(line, sizeof line, "{\"time\":%lld.%09ld,\"event\":\"%s\",\"uid\":%lu,\"request\":%ld,\"status\":%d,\"requested_executable\":%s,\"resolved_executable\":%s,\"requested_executable_truncated\":%s,\"resolved_executable_truncated\":%s}\n", (long long)ts.tv_sec, ts.tv_nsec, event, (unsigned long)uid, (long)pid, status, requested, resolved, requested_truncated ? "true" : "false", resolved_truncated ? "true" : "false");
    if (n < 0 || (size_t)n >= sizeof line || flock(audit_fd, LOCK_EX) < 0) return -1;
    int rc = safe_file(audit_fd, 0600) && full_write(audit_fd, line, (size_t)n) == 0 && fsync(audit_fd) == 0 ? 0 : -1;
    if (flock(audit_fd, LOCK_UN) < 0) rc = -1;
    return rc;
}
static void send_reply(int fd, int status) {
    struct reply r = {htonl(MAGIC), htonl((uint32_t)status)};
    (void)full_write(fd, &r, sizeof r);
}
static int token_matches(uid_t uid, const unsigned char token[TOKEN_SIZE]) {
    char path[PATH_MAX]; unsigned char expected[TOKEN_SIZE]; struct stat st;
    int d = open(policy_path, O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC);
    if (d < 0) return 0;
    if (fstat(d, &st) < 0 || st.st_uid != owner_uid || (st.st_mode & 0777) != 0700) { close(d); return 0; }
    int n = snprintf(path, sizeof path, "%lu", (unsigned long)uid);
    int fd = n > 0 && (size_t)n < sizeof path ? openat(d, path, O_RDONLY | O_NOFOLLOW | O_NONBLOCK | O_CLOEXEC) : -1;
    close(d);
    if (fd < 0) return 0;
    bool valid = safe_file(fd, 0600) && fstat(fd, &st) == 0 && st.st_size == TOKEN_SIZE && full_read(fd, expected, sizeof expected) == 0;
    close(fd);
    unsigned int diff = 0;
    if (valid) for (size_t i = 0; i < TOKEN_SIZE; i++) diff |= expected[i] ^ token[i];
    erase(expected, sizeof expected);
    return valid && diff == 0;
}
static int receive_request(int sock, struct request *r, int fds[3]) {
    char control[CMSG_SPACE(sizeof(int) * 8)] = {0};
    struct iovec io = {r, sizeof *r};
    struct msghdr m = {.msg_iov = &io, .msg_iovlen = 1, .msg_control = control, .msg_controllen = sizeof control};
    ssize_t n;
    do { n = recvmsg(sock, &m, MSG_WAITALL | MSG_CMSG_CLOEXEC); } while (n < 0 && errno == EINTR);
    if (n < 0) return -1;
    int count = 0;
    for (struct cmsghdr *c = CMSG_FIRSTHDR(&m); c; c = CMSG_NXTHDR(&m, c)) {
        if (c->cmsg_level != SOL_SOCKET || c->cmsg_type != SCM_RIGHTS || c->cmsg_len < CMSG_LEN(0)) continue;
        size_t total = (c->cmsg_len - CMSG_LEN(0)) / sizeof(int);
        int *received = (int *)CMSG_DATA(c);
        for (size_t i = 0; i < total; i++) { if (count < 3) fds[count] = received[i]; else close(received[i]); count++; }
    }
    return n == sizeof *r && !(m.msg_flags & (MSG_CTRUNC | MSG_TRUNC)) && count == 3 ? 0 : -1;
}
static bool valid_header(const struct request *r) {
    uint32_t argc = ntohl(r->argc), bytes = ntohl(r->bytes);
    return ntohl(r->magic) == MAGIC && ntohl(r->version) == 1 && argc > 0 && argc <= MAX_ARGS && bytes >= argc && bytes <= MAX_PAYLOAD;
}
static int decode_args(struct request *r, char *payload, char *args[MAX_ARGS + 1]) {
    uint32_t argc = ntohl(r->argc), bytes = ntohl(r->bytes);
    if (!valid_header(r)) return -1;
    size_t offset = 0;
    for (uint32_t i = 0; i < argc; i++) {
        if (offset >= bytes) return -1;
        size_t n = strnlen(payload + offset, bytes - offset);
        if (n > MAX_ARG || n == bytes - offset || (i == 0 && n == 0)) return -1;
        args[i] = payload + offset; offset += n + 1;
    }
    args[argc] = NULL;
    return offset == bytes ? 0 : -1;
}
static void close_fds(int fds[3]) { for (int i = 0; i < 3; i++) if (fds[i] >= 3) close(fds[i]); }
static int exit_status(int status) { return WIFEXITED(status) ? WEXITSTATUS(status) : WIFSIGNALED(status) ? 128 + WTERMSIG(status) : 125; }
static void exec_command(char *const args[]) {
    extern char **environ;
    if (strchr(args[0], '/')) { execve(args[0], args, environ); return; }
    int saved_error = ENOENT;
    for (size_t i = 0; i < sizeof system_dirs / sizeof system_dirs[0]; i++) {
        char executable[PATH_MAX];
        if (snprintf(executable, sizeof executable, "%s/%s", system_dirs[i], args[0]) >= (int)sizeof executable) { errno = ENAMETOOLONG; return; }
        execve(executable, args, environ);
        if (errno == EACCES) saved_error = EACCES;
        else if (errno != ENOENT && errno != ENOTDIR) return;
    }
    errno = saved_error;
}
static void resolve_executable(const char *requested) {
    audit_resolved[0] = 0;
    size_t attempts = strchr(requested, '/') ? 1 : sizeof system_dirs / sizeof system_dirs[0];
    for (size_t i = 0; i < attempts; i++) {
        char candidate[PATH_MAX];
        int n = strchr(requested, '/')
            ? snprintf(candidate, sizeof candidate, "%s%s", requested[0] == '/' ? "" : "/", requested)
            : snprintf(candidate, sizeof candidate, "%s/%s", system_dirs[i], requested);
        if (n < 0 || (size_t)n >= sizeof candidate) return;
        struct stat st;
        if (realpath(candidate, audit_resolved) && stat(audit_resolved, &st) == 0 && S_ISREG(st.st_mode) && access(audit_resolved, X_OK) == 0) return;
        audit_resolved[0] = 0;
    }
}
static void execute_request(int sock) {
    struct ucred peer; socklen_t plen = sizeof peer;
    if (getsockopt(sock, SOL_SOCKET, SO_PEERCRED, &peer, &plen) < 0) return;
    struct timeval timeout = {.tv_sec = 5};
    (void)setsockopt(sock, SOL_SOCKET, SO_RCVTIMEO, &timeout, sizeof timeout);
    (void)setsockopt(sock, SOL_SOCKET, SO_SNDTIMEO, &timeout, sizeof timeout);
    int64_t deadline = monotonic_millis();
    if (deadline < 0) return;
    deadline += 5000;
    struct request r = {0}; int fds[3] = {-1, -1, -1}; char *payload = NULL; char *args[MAX_ARGS + 1] = {0};
    int result = 126;
    if (receive_request(sock, &r, fds) < 0 || !valid_header(&r)) goto denied;
    if (!token_matches(peer.uid, r.token)) goto denied;
    erase(r.token, sizeof r.token);
    uint32_t bytes = ntohl(r.bytes);
    send_reply(sock, 0); /* Authenticated header acknowledgment, before receiving argv. */
    payload = malloc(bytes);
    if (!payload || read_request_payload(sock, payload, bytes, deadline) < 0) goto denied;
    int decoded = decode_args(&r, payload, args);
    if (args[0]) { audit_requested = args[0]; resolve_executable(args[0]); }
    if (decoded < 0) goto denied;
    if (audit("allow", peer.uid, getpid(), 0) < 0) { result = 125; goto done; }
    /* Audit start before creating a process: audit failures can never launch a command. */
    if (audit("start", peer.uid, getpid(), 0) < 0) { result = 125; goto done; }
    int ready[2];
    if (pipe2(ready, O_CLOEXEC) < 0) { result = 125; goto finished; }
    pid_t worker_pid = getpid();
    pid_t child = fork();
    if (child == 0) {
        close(ready[0]);
        if (setsid() < 0) _exit(125);
        for (int i = 0; i < 3; i++) if (dup2(fds[i], i) < 0) _exit(125);
        close_fds(fds); close(sock); close(audit_fd);
#ifndef AROOT_TESTING
        if (setgroups(0, NULL) < 0 || setresgid(0, 0, 0) < 0 || setresuid(0, 0, 0) < 0) _exit(125);
#endif
        /* Credential changes can clear the parent-death setting; set it last. */
        if (prctl(PR_SET_PDEATHSIG, SIGKILL) < 0 || getppid() != worker_pid) _exit(125);
        umask(022);
        if (chdir("/") < 0 || clearenv() < 0 || setenv("PATH", "/usr/sbin:/usr/bin:/sbin:/bin", 1) < 0 || setenv("HOME", "/root", 1) < 0 || setenv("USER", "root", 1) < 0 || setenv("LOGNAME", "root", 1) < 0 || setenv("LANG", "C.UTF-8", 1) < 0 || setenv("TERM", "dumb", 1) < 0) _exit(125);
        struct sigaction sa = {.sa_handler = SIG_DFL}; sigemptyset(&sa.sa_mask);
        for (int sig = 1; sig < NSIG; sig++) (void)sigaction(sig, &sa, NULL);
        if (full_write(ready[1], "R", 1) < 0) _exit(125);
        close(ready[1]);
        if (close_range(3, ~0U, 0) < 0) {
            long limit = sysconf(_SC_OPEN_MAX);
            if (limit < 0 || limit > INT_MAX) _exit(125);
            for (int fd = 3; fd < limit; fd++) close(fd);
        }
        exec_command(args);
        int exec_error = errno;
        dprintf(STDERR_FILENO, "aroot: cannot execute %s: %s\n", args[0], strerror(exec_error));
        _exit(exec_error == ENOENT ? 127 : 126);
    }
    close(ready[1]); close_fds(fds); fds[0] = fds[1] = fds[2] = -1;
    if (child < 0) { close(ready[0]); result = 125; goto finished; }
    char ready_byte;
    (void)full_read(ready[0], &ready_byte, 1); close(ready[0]);
    int status = 0; bool disconnected = false; time_t disconnected_at = 0;
    for (;;) {
        pid_t done_pid = waitpid(child, &status, WNOHANG);
        if (done_pid == child) break;
        if (done_pid < 0 && errno != EINTR) { status = 125 << 8; break; }
        struct pollfd p = {.fd = disconnected ? -1 : sock, .events = POLLIN};
        int polled = poll(&p, 1, 50);
        if (polled > 0 && (p.revents & (POLLIN | POLLHUP | POLLERR | POLLNVAL))) {
            unsigned char sig;
            ssize_t n = recv(sock, &sig, 1, MSG_DONTWAIT);
            if (n == 1 && (sig == SIGINT || sig == SIGQUIT || sig == SIGTERM || sig == SIGHUP || sig == SIGWINCH)) (void)kill(-child, sig);
            else { disconnected = true; disconnected_at = time(NULL); (void)kill(-child, SIGTERM); }
        }
        if (disconnected && time(NULL) - disconnected_at >= 2) (void)kill(-child, SIGKILL);
    }
    result = exit_status(status);
finished:
    if (audit("finish", peer.uid, getpid(), result) < 0) result = 125;
    goto done;
denied:
    if (audit("deny", peer.uid, getpid(), 126) < 0) result = 125;
done:
    erase(r.token, sizeof r.token); close_fds(fds); audit_requested = NULL; audit_resolved[0] = 0; free(payload); send_reply(sock, result);
}
static void daemon_signal(int sig) { if (sig == SIGCHLD) child_changed = 1; else stopping = 1; }
static int daemon_main(int argc, char **argv) {
#ifdef AROOT_TESTING
    if (geteuid() == 0 || argc != 5 || strcmp(argv[1], "--test-paths") != 0) { fprintf(stderr, "aroot: test daemon requires nonroot and explicit private paths\n"); return 125; }
    socket_path = argv[2]; policy_path = argv[3]; audit_path = argv[4];
#else
    if (geteuid() != 0 || argc != 1) { fprintf(stderr, "arootd must run as root with no arguments\n"); return 125; }
    (void)argv;
#endif
    owner_uid = geteuid();
    if (!safe_dir(policy_path, 0700)) { fprintf(stderr, "aroot: unsafe policy directory\n"); return 125; }
    audit_fd = open_audit();
    if (audit_fd < 0 || !safe_file(audit_fd, 0600) || audit("daemon_start", owner_uid, getpid(), 0) < 0) { fprintf(stderr, "aroot: required audit unavailable or unsafe\n"); return 125; }
    char parent[PATH_MAX];
    if (strlen(socket_path) >= sizeof parent) return 125;
    strcpy(parent, socket_path); char *slash = strrchr(parent, '/');
    if (!slash || slash == parent) return 125;
    *slash = '\0';
    if (!safe_dir(parent, 0750)
#ifdef AROOT_TESTING
        && !safe_dir(parent, 0700)
#endif
    ) { fprintf(stderr, "aroot: unsafe socket directory\n"); return 125; }
    char lock_path[PATH_MAX];
    if (snprintf(lock_path, sizeof lock_path, "%s/daemon.lock", parent) >= (int)sizeof lock_path) return 125;
    int lock_fd = open(lock_path, O_RDWR | O_CREAT | O_NOFOLLOW | O_NONBLOCK | O_CLOEXEC, 0600);
    if (lock_fd < 0 || !safe_file(lock_fd, 0600) || flock(lock_fd, LOCK_EX | LOCK_NB) < 0) { fprintf(stderr, "aroot: daemon already running or unsafe lock\n"); return 125; }
    struct stat st;
    if (lstat(socket_path, &st) == 0) { if (!S_ISSOCK(st.st_mode) || st.st_uid != owner_uid || unlink(socket_path) < 0) die("existing socket"); }
    else if (errno != ENOENT) die("socket path");
    int listener = socket(AF_UNIX, SOCK_STREAM | SOCK_CLOEXEC, 0);
    if (listener < 0) die("socket");
    struct sockaddr_un addr = {.sun_family = AF_UNIX};
    if (strlen(socket_path) >= sizeof addr.sun_path) { fprintf(stderr, "socket path too long\n"); return 125; }
    strcpy(addr.sun_path, socket_path); umask(0077);
    if (bind(listener, (struct sockaddr *)&addr, sizeof addr) < 0) die("bind");
#ifndef AROOT_TESTING
    struct group *gr = getgrnam("aroot");
    if (!gr || chown(socket_path, 0, gr->gr_gid) < 0) die("socket group aroot");
#endif
    if (chmod(socket_path, 0660) < 0 || listen(listener, MAX_WORKERS) < 0) die("listen");
    struct sigaction sa = {.sa_handler = daemon_signal}; sigemptyset(&sa.sa_mask);
    if (sigaction(SIGTERM, &sa, NULL) < 0 || sigaction(SIGINT, &sa, NULL) < 0 || sigaction(SIGCHLD, &sa, NULL) < 0) die("sigaction");
    signal(SIGPIPE, SIG_IGN);
    int active = 0;
    while (!stopping) {
        if (child_changed) { child_changed = 0; while (waitpid(-1, NULL, WNOHANG) > 0) active--; }
        struct pollfd p = {.fd = listener, .events = POLLIN};
        if (poll(&p, 1, 200) <= 0) continue;
        int sock = accept4(listener, NULL, NULL, SOCK_CLOEXEC);
        if (sock < 0) continue;
        if (active >= MAX_WORKERS) { struct ucred peer; socklen_t len = sizeof peer; if (getsockopt(sock, SOL_SOCKET, SO_PEERCRED, &peer, &len) == 0) (void)audit("deny_busy", peer.uid, getpid(), 126); close(sock); continue; }
        pid_t child = fork();
        if (child == 0) {
            close(listener); close(lock_fd); signal(SIGCHLD, SIG_DFL); signal(SIGTERM, SIG_DFL); signal(SIGINT, SIG_DFL);
            close(audit_fd);
            audit_fd = open_audit();
            execute_request(sock); close(sock); close(audit_fd); _exit(0);
        }
        if (child < 0) { struct ucred peer; socklen_t len = sizeof peer; if (getsockopt(sock, SOL_SOCKET, SO_PEERCRED, &peer, &len) == 0) (void)audit("deny_fork", peer.uid, getpid(), 125); }
        if (child > 0) active++;
        close(sock);
    }
    close(listener); unlink(socket_path); close(lock_fd);
    (void)audit("daemon_stop", owner_uid, getpid(), 0); close(audit_fd); return 0;
}
static void client_signal(int sig) { pending_signal = sig; }
static int load_token(int fd, unsigned char token[TOKEN_SIZE], bool check) {
    struct stat st;
    if (check && (fstat(fd, &st) < 0 || !S_ISREG(st.st_mode) || st.st_uid != getuid() || (st.st_mode & 0777) != 0600 || st.st_size != TOKEN_SIZE || st.st_nlink != 1)) return -1;
    return full_read(fd, token, TOKEN_SIZE);
}
static int client_main(int argc, char **argv) {
    if (argc == 2 && strcmp(argv[1], "--help") == 0) { puts("usage: aroot [--token-file PATH | --token-fd FD] [--] COMMAND [ARG...]"); return 0; }
    int at = 1, token_fd = -1; const char *token_file = NULL;
#ifdef AROOT_TESTING
    if (argc < 5 || strcmp(argv[1], "--test-socket") != 0 || geteuid() == 0) return 125;
    socket_path = argv[2]; at = 3;
#endif
    if (at < argc && strcmp(argv[at], "--token-file") == 0 && at + 1 < argc) { token_file = argv[at + 1]; at += 2; }
    else if (at < argc && strcmp(argv[at], "--token-fd") == 0 && at + 1 < argc) {
        char *end; long n = strtol(argv[at + 1], &end, 10);
        if (*end || n < 3 || n > INT_MAX) { fprintf(stderr, "aroot: token FD must be an open descriptor >= 3\n"); return 125; }
        token_fd = (int)n; at += 2;
    }
    if (at < argc && strcmp(argv[at], "--") == 0) at++;
    if (at == argc || argc - at > (int)MAX_ARGS) { fprintf(stderr, "usage: aroot [--token-file PATH | --token-fd FD] [--] COMMAND [ARG...]\n"); return 125; }
    char default_token[PATH_MAX];
    if (!token_file && token_fd < 0) {
        struct passwd *pw = getpwuid(getuid());
        if (!pw || snprintf(default_token, sizeof default_token, "%s/.config/aroot/token", pw->pw_dir) >= (int)sizeof default_token) return 125;
        token_file = default_token;
    }
    struct request r = {.magic = htonl(MAGIC), .version = htonl(1), .argc = htonl((uint32_t)(argc - at))};
    if (token_file) token_fd = open(token_file, O_RDONLY | O_NOFOLLOW | O_NONBLOCK | O_CLOEXEC);
    if (token_fd < 0 || load_token(token_fd, r.token, token_file != NULL) < 0) { fprintf(stderr, "aroot: token missing or unsafe (expected a private 32-byte token)\n"); if (token_fd >= 0) close(token_fd); return 126; }
    close(token_fd);
    size_t bytes = 0;
    for (int i = at; i < argc; i++) { size_t n = strlen(argv[i]); if (n > MAX_ARG || bytes + n + 1 > MAX_PAYLOAD) { erase(r.token, sizeof r.token); fprintf(stderr, "aroot: argument limit exceeded\n"); return 125; } bytes += n + 1; }
    if (!argv[at][0]) { erase(r.token, sizeof r.token); return 125; }
    r.bytes = htonl((uint32_t)bytes);
    int sock = socket(AF_UNIX, SOCK_STREAM | SOCK_CLOEXEC, 0);
    if (sock < 0) die("socket");
    struct sockaddr_un addr = {.sun_family = AF_UNIX};
    if (strlen(socket_path) >= sizeof addr.sun_path) return 125;
    strcpy(addr.sun_path, socket_path);
    if (connect(sock, (struct sockaddr *)&addr, sizeof addr) < 0) { erase(r.token, sizeof r.token); perror("aroot: connect"); close(sock); return 125; }
    struct ucred peer; socklen_t plen = sizeof peer;
    uid_t expected_uid = 0;
#ifdef AROOT_TESTING
    expected_uid = getuid();
#endif
    if (getsockopt(sock, SOL_SOCKET, SO_PEERCRED, &peer, &plen) < 0 || peer.uid != expected_uid) { erase(r.token, sizeof r.token); fprintf(stderr, "aroot: untrusted service owner\n"); close(sock); return 125; }
    struct sigaction sa = {.sa_handler = client_signal}; sigemptyset(&sa.sa_mask);
    int sigs[] = {SIGINT, SIGQUIT, SIGTERM, SIGHUP, SIGWINCH};
    for (size_t i = 0; i < sizeof sigs / sizeof sigs[0]; i++) if (sigaction(sigs[i], &sa, NULL) < 0) die("sigaction");
    signal(SIGPIPE, SIG_IGN);
    char control[CMSG_SPACE(sizeof(int) * 3)] = {0}; struct iovec io = {&r, sizeof r};
    struct msghdr m = {.msg_iov = &io, .msg_iovlen = 1, .msg_control = control, .msg_controllen = sizeof control};
    struct cmsghdr *c = CMSG_FIRSTHDR(&m); c->cmsg_level = SOL_SOCKET; c->cmsg_type = SCM_RIGHTS; c->cmsg_len = CMSG_LEN(sizeof(int) * 3);
    int fds[3] = {STDIN_FILENO, STDOUT_FILENO, STDERR_FILENO}; memcpy(CMSG_DATA(c), fds, sizeof fds);
    ssize_t sent = sendmsg(sock, &m, MSG_NOSIGNAL);
    erase(r.token, sizeof r.token);
    if (sent != sizeof r) { perror("aroot: send"); close(sock); return 125; }
    struct reply acknowledgment;
    if (full_read(sock, &acknowledgment, sizeof acknowledgment) < 0 || ntohl(acknowledgment.magic) != MAGIC || ntohl(acknowledgment.status) > 255) { fprintf(stderr, "aroot: authentication protocol failed\n"); close(sock); return 125; }
    if (ntohl(acknowledgment.status) != 0) { int status = (int)ntohl(acknowledgment.status); fprintf(stderr, "aroot: authorization denied or required audit failed\n"); close(sock); return status; }
    for (int i = at; i < argc; i++) if (full_write(sock, argv[i], strlen(argv[i]) + 1) < 0) { fprintf(stderr, "aroot: request rejected\n"); close(sock); return 126; }
    for (;;) {
        if (pending_signal) { unsigned char sig = (unsigned char)pending_signal; pending_signal = 0; if (full_write(sock, &sig, 1) < 0) break; }
        struct pollfd p = {.fd = sock, .events = POLLIN};
        int n = poll(&p, 1, 100);
        if (n < 0 && errno == EINTR) continue;
        if (n < 0) break;
        if (n > 0 && p.revents) {
            struct reply reply;
            if (full_read(sock, &reply, sizeof reply) < 0 || ntohl(reply.magic) != MAGIC || ntohl(reply.status) > 255) break;
            int status = (int)ntohl(reply.status); close(sock);
            if (status == 126) fprintf(stderr, "aroot: authorization denied or command not executable\n");
            if (status == 125) fprintf(stderr, "aroot: service or required audit failed\n");
            return status;
        }
    }
    fprintf(stderr, "aroot: service disconnected\n"); close(sock); return 125;
}
static int parse_uid(const char *s, uid_t *uid) {
    if (!*s) return -1;
    for (const char *p = s; *p; p++) if (*p < '0' || *p > '9') return -1;
    char *end; errno = 0; unsigned long n = strtoul(s, &end, 10);
    if (errno || *end || n > UINT32_MAX || (uid_t)n == (uid_t)-1) return -1;
    *uid = (uid_t)n; return 0;
}
static int token_parent(const char *path, uid_t uid, char name[NAME_MAX + 1]) {
    if (path[0] != '/' || strlen(path) >= PATH_MAX || path[strlen(path) - 1] == '/') return -1;
    char copy[PATH_MAX]; strcpy(copy, path + 1);
    int dir = open("/", O_RDONLY | O_DIRECTORY | O_CLOEXEC);
    char *save = NULL, *part = strtok_r(copy, "/", &save);
    if (dir < 0 || !part) { if (dir >= 0) close(dir); return -1; }
    for (;;) {
        char *next = strtok_r(NULL, "/", &save);
        if (!strcmp(part, ".") || !strcmp(part, "..") || strlen(part) > NAME_MAX) { close(dir); return -1; }
        if (!next) { strcpy(name, part); return dir; }
        int child = openat(dir, part, O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC);
        close(dir);
        struct stat st;
        if (child < 0) return -1;
        if (fstat(child, &st) < 0 || (st.st_uid != 0 && st.st_uid != uid) || (st.st_mode & 0022)) { close(child); return -1; }
        dir = child; part = next;
    }
}
static int sync_policy_directory(void) {
    int fd = open(POLICY_PATH, O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC);
    if (fd < 0) return -1;
    int rc = fsync(fd); close(fd); return rc;
}
static int admin_main(int argc, char **argv) {
    if (geteuid() != 0) { fprintf(stderr, "aroot-admin requires root\n"); return 125; }
    owner_uid = 0; uid_t uid;
    if (argc < 3 || parse_uid(argv[2], &uid) < 0 || !safe_dir(POLICY_PATH, 0700)) { fprintf(stderr, "usage: aroot-admin grant UID NEW_TOKEN_FILE | revoke UID\n"); return 125; }
    char policy[PATH_MAX];
    if (snprintf(policy, sizeof policy, "%s/%lu", POLICY_PATH, (unsigned long)uid) >= (int)sizeof policy) return 125;
    audit_fd = open_audit();
    if (audit_fd < 0) { fprintf(stderr, "aroot-admin: required audit unavailable\n"); return 125; }
    if (strcmp(argv[1], "revoke") == 0 && argc == 3) {
        if (audit("revoke", uid, getpid(), 0) < 0) { fprintf(stderr, "aroot-admin: required audit failed\n"); return 125; }
        if (unlink(policy) < 0 && errno != ENOENT) die("revoke");
        if (sync_policy_directory() < 0) die("persist revocation");
        printf("Revoked UID %lu for new requests. Existing commands continue.\n", (unsigned long)uid); return 0;
    }
    if (strcmp(argv[1], "grant") != 0 || argc != 4 || argv[3][0] != '/' || !getpwuid(uid)) { fprintf(stderr, "aroot-admin: grant requires an existing UID and absolute new token path\n"); return 125; }
    char name[NAME_MAX + 1];
    int token_dir = token_parent(argv[3], uid, name);
    if (token_dir < 0) { fprintf(stderr, "aroot-admin: token parents must be nonsymlink directories owned by root or target UID, with no group/other write access\n"); return 125; }
    if (audit("grant_request", uid, getpid(), 0) < 0) { close(token_dir); fprintf(stderr, "aroot-admin: required audit failed\n"); return 125; }
    unsigned char token[TOKEN_SIZE]; size_t got = 0;
    while (got < sizeof token) { ssize_t n = getrandom(token + got, sizeof token - got, 0); if (n < 0 && errno == EINTR) continue; if (n <= 0) die("getrandom"); got += (size_t)n; }
    /* O_EXCL avoids replacing a token through a symlink. Policy grants also require explicit revoke before rotation. */
    int p = open(policy, O_WRONLY | O_CREAT | O_EXCL | O_NOFOLLOW | O_CLOEXEC, 0600);
    if (p < 0) { erase(token, sizeof token); die("grant policy (revoke existing grant first)"); }
    int out = openat(token_dir, name, O_WRONLY | O_CREAT | O_EXCL | O_NOFOLLOW | O_CLOEXEC, 0600);
    if (out < 0) { close(token_dir); unlink(policy); erase(token, sizeof token); die("new token file"); }
    struct passwd *pw = getpwuid(uid);
    int ok = pw && fchown(out, uid, pw->pw_gid) == 0 && full_write(out, token, sizeof token) == 0 && fsync(out) == 0 && full_write(p, token, sizeof token) == 0 && fsync(p) == 0 && fsync(token_dir) == 0 && sync_policy_directory() == 0;
    erase(token, sizeof token); close(out); close(p);
    if (!ok) { unlinkat(token_dir, name, 0); close(token_dir); unlink(policy); fprintf(stderr, "aroot-admin: grant failed\n"); return 125; }
    if (audit("grant", uid, getpid(), 0) < 0) { unlinkat(token_dir, name, 0); close(token_dir); unlink(policy); fprintf(stderr, "aroot-admin: required audit failed; grant rolled back\n"); return 125; }
    close(token_dir);
    printf("Granted unrestricted command authority to UID %lu. Token written privately; add user to group aroot.\n", (unsigned long)uid);
    return 0;
}
int main(int argc, char **argv) {
    if (argc == 2 && strcmp(argv[1], "--version") == 0) { puts("aroot " VERSION); return 0; }
    /* Tokens and command memory must never enter a crash dump. */
    if (prctl(PR_SET_DUMPABLE, 0) < 0) die("disable core dumps");
    for (int fd = 0; fd < 3; fd++) if (fcntl(fd, F_GETFD) < 0) { int replacement = open("/dev/null", fd == 0 ? O_RDONLY : O_WRONLY); if (replacement != fd) die("standard descriptor"); }
    const char *base = strrchr(argv[0], '/'); base = base ? base + 1 : argv[0];
    if (strcmp(base, "arootd") == 0 || strcmp(base, "arootd-test") == 0) return daemon_main(argc, argv);
    if (strcmp(base, "aroot-admin") == 0) return admin_main(argc, argv);
    return client_main(argc, argv);
}
