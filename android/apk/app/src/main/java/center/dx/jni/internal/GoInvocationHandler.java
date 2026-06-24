package center.dx.jni.internal;

import java.lang.reflect.InvocationHandler;
import java.lang.reflect.Method;

/**
 * GoInvocationHandler is a Java-side InvocationHandler that delegates
 * method calls to a Go callback identified by a long ID.
 *
 * The invoke() method is native; the Go c-shared library registers
 * its implementation via JNI RegisterNatives during proxy initialization.
 */
public class GoInvocationHandler implements InvocationHandler {
    private final long handlerID;

    public GoInvocationHandler(long handlerID) {
        this.handlerID = handlerID;
    }

    public long getHandlerID() {
        return handlerID;
    }

    @Override
    public native Object invoke(Object proxy, Method method, Object[] args)
            throws Throwable;
}
