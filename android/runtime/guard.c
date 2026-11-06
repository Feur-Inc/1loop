// SPDX-License-Identifier: MPL-2.0

#include <errno.h>
#include <signal.h>
#include <fcntl.h>
#include <string.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/prctl.h>
#include <sys/ioctl.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>

/* PRoot must die with the Android app so its EXITKILL policy also ends the
 * traced process tree.
 *
 * PR_SET_PDEATHSIG fires when the THREAD that started a process ends, not
 * only its process: set here, PRoot was killed whenever the Java thread that
 * started it (a pool thread, retired when idle) ended while the app ran on.
 * So the guard stays, watches the app's process and starts the program as
 * its own child: the child's PDEATHSIG names the guard, which has a single
 * thread, and the guard kills it once the app's process is gone. */

static volatile pid_t child = -1;

static void forward(int signal) {
    if (child > 0) kill(child, signal);
}

int main(int argc, char **argv) {
    if (argc < 3) return 2;
    char *end;
    long parent = strtol(argv[1], &end, 10);
    if (*end || parent <= 0) return 2;
    if (getppid() != parent) return 1;
    int program = 2;
    const char *tty = NULL;
    if (strcmp(argv[program], "--pty") == 0) {
        if (argc < 5 || strncmp(argv[3], "/dev/pts/", 9) != 0 ||
            argv[3][9] == '\0' || strspn(argv[3] + 9, "0123456789") != strlen(argv[3] + 9)) return 2;
        tty = argv[3];
        program = 4;
    }
    /* The child waits on this pipe until its pid is printed: its own output
     * must come after the header. */
    int ready[2];
    if (pipe(ready) != 0) {
        perror("pipe");
        return 1;
    }
    pid_t guard = getpid();
    child = fork();
    if (child < 0) {
        perror("fork");
        return 1;
    }
    if (child == 0) {
        close(ready[1]);
        if (prctl(PR_SET_PDEATHSIG, SIGKILL) != 0) {
            perror("PR_SET_PDEATHSIG");
            _exit(1);
        }
        if (getppid() != guard) _exit(1);
        char go;
        if (read(ready[0], &go, 1) != 1) _exit(1);
        close(ready[0]);
        if (tty != NULL) {
            int terminal = open(tty, O_RDWR | O_NOCTTY);
            if (terminal < 0 || setsid() < 0 || ioctl(terminal, TIOCSCTTY, 0) < 0) {
                perror("terminal session");
                _exit(1);
            }
            for (int descriptor = 0; descriptor <= 2; descriptor++) {
                if (dup2(terminal, descriptor) < 0) _exit(1);
            }
            if (terminal > 2) close(terminal);
        }
        execv(argv[program], argv + program);
        perror("execv");
        _exit(1);
    }
    close(ready[0]);
    struct sigaction action;
    memset(&action, 0, sizeof action);
    action.sa_handler = forward;
    sigaction(SIGTERM, &action, NULL);
    sigaction(SIGINT, &action, NULL);
    sigaction(SIGHUP, &action, NULL);
    printf("1loop-child:%ld\n", (long)child);
    fflush(stdout);
    if (write(ready[1], "", 1) != 1) {
        kill(child, SIGKILL);
    }
    close(ready[1]);
    /* The program's input and output are its own: the guard keeps none of
     * them open, so the app sees their end when the program closes them. */
    int nothing = open("/dev/null", O_RDWR);
    if (nothing >= 0) {
        for (int descriptor = 0; descriptor <= 2; descriptor++) dup2(nothing, descriptor);
        if (nothing > 2) close(nothing);
    }
    const struct timespec pause = {0, 250 * 1000 * 1000};
    for (;;) {
        int status;
        pid_t done = waitpid(child, &status, WNOHANG);
        if (done == child) {
            if (WIFSIGNALED(status)) {
                /* End the same way, so the app reads the same status. */
                signal(WTERMSIG(status), SIG_DFL);
                kill(getpid(), WTERMSIG(status));
            }
            return WIFEXITED(status) ? WEXITSTATUS(status) : 1;
        }
        if (done < 0 && errno != EINTR) return 1;
        if (getppid() != parent) { /* the app's process is gone */
            kill(child, SIGKILL);
            waitpid(child, &status, 0);
            return 1;
        }
        nanosleep(&pause, NULL);
    }
}
