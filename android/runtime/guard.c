#include <signal.h>
#include <fcntl.h>
#include <string.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/prctl.h>
#include <sys/ioctl.h>
#include <unistd.h>

/* PRoot must die with the Android service so its EXITKILL policy also ends
 * the traced process tree. Check the parent after prctl to close the race. */
int main(int argc, char **argv) {
    if (argc < 3) return 2;
    char *end;
    long parent = strtol(argv[1], &end, 10);
    if (*end || parent <= 0) return 2;
    if (prctl(PR_SET_PDEATHSIG, SIGKILL) != 0) {
        perror("PR_SET_PDEATHSIG");
        return 1;
    }
    if (getppid() != parent) return 1;
    int program = 2;
    int terminal = -1;
    if (strcmp(argv[program], "--pty") == 0) {
        if (argc < 5 || strncmp(argv[3], "/dev/pts/", 9) != 0 ||
            argv[3][9] == '\0' || strspn(argv[3] + 9, "0123456789") != strlen(argv[3] + 9)) return 2;
        terminal = open(argv[3], O_RDWR | O_NOCTTY);
        if (terminal < 0 || setsid() < 0 || ioctl(terminal, TIOCSCTTY, 0) < 0) {
            perror("terminal session");
            return 1;
        }
        program = 4;
    }
    printf("1loop-child:%ld\n", (long)getpid());
    fflush(stdout);
    if (terminal >= 0) {
        for (int descriptor = 0; descriptor <= 2; descriptor++) {
            if (dup2(terminal, descriptor) < 0) return 1;
        }
        if (terminal > 2) close(terminal);
    }
    execv(argv[program], argv + program);
    perror("execv");
    return 1;
}
