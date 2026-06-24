package center.dx.jni.internal;

/**
 * GoAbstractDispatch is a helper class for dispatching abstract class
 * method calls to Go callbacks identified by a long handler ID.
 *
 * The invoke() method is static native; the Go c-shared library registers
 * its implementation via JNI RegisterNatives during proxy initialization.
 */
public class GoAbstractDispatch {
    public static native Object invoke(long handlerID, String methodName, Object[] args);
}
