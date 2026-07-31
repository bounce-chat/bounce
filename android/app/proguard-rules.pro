# gomobile generates JNI stubs whose classes and native methods are looked up by
# name from Go. Keep everything the bind layer produces, plus the Seq runtime.
-keep class go.** { *; }
-keep class chat.bounce.goengine.** { *; }
-keepclasseswithmembernames class * {
    native <methods>;
}

# Kotlin classes implementing Go interfaces (the event sink) are instantiated on
# the Kotlin side but invoked from Go through the generated proxies.
-keep class chat.bounce.engine.** { *; }
