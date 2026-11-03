package io.oneloop.remote;
import android.os.ParcelFileDescriptor;
interface IPhoneShell {
    ParcelFileDescriptor execute(String command, int limit) = 1;
    int openHiddenDisplay(int width, int height, int density) = 2;
    ParcelFileDescriptor captureHiddenDisplay() = 3;
    void closeHiddenDisplay() = 4;
    void destroy() = 16777114;
}
