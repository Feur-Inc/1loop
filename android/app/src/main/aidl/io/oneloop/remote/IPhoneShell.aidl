package io.oneloop.remote;
import android.os.ParcelFileDescriptor;
interface IPhoneShell {
    ParcelFileDescriptor execute(String command, int limit) = 1;
    void destroy() = 16777114;
}
