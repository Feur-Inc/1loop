#include <errno.h>
#include <pthread.h>
#include <stdint.h>
#include <stdlib.h>
#include <sys/random.h>

/* Linked into the static helpers in place of libc's arc4random: the NDK's
 * static libc requires MADV_WIPEONFORK (Linux 4.14) and aborts on older
 * kernels, such as a Pixel 3a's 4.9. These are every symbol its member
 * defines, so the linker never takes it. */

void arc4random_buf(void *buffer, size_t size) {
    unsigned char *at = buffer;
    while (size > 0) {
        ssize_t got = getrandom(at, size, 0);
        if (got < 0) {
            if (errno == EINTR) {
                continue;
            }
            abort();
        }
        at += got;
        size -= (size_t)got;
    }
}

uint32_t arc4random(void) {
    uint32_t value;
    arc4random_buf(&value, sizeof value);
    return value;
}

static pthread_mutex_t lock = PTHREAD_MUTEX_INITIALIZER;

void arc4random_mutex_lock(void) { pthread_mutex_lock(&lock); }

void arc4random_mutex_unlock(void) { pthread_mutex_unlock(&lock); }
