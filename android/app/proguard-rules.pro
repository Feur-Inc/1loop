# Conscrypt bundles legacy socket adapters used only below API 22. Their hidden
# classes are absent from modern SDK stubs; 1loop requires Android 8 or later.
-dontwarn com.android.org.conscrypt.SSLParametersImpl
-dontwarn org.apache.harmony.xnet.provider.jsse.SSLParametersImpl
