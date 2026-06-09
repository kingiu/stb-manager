#!/system/bin/sh

FIRST_BOOT=$(getprop ro.boot.firstboot)
BUILD_DATE=$(getprop ro.build.date.utc)
MARK=/data/local/symbol_thirdpart_apks_installed
PKGS=/system/preinstall/

output_log() {
    log -p e -t "preinstall" "SCRIPT: " "$@"
}

upgrade_reinstall() {
    if [ -e $MARK ]; then
        BEFORE_DATE=$(< $MARK)
    fi

    if [[ ! -e $MARK ]] || [[ $FIRST_BOOT -eq 1 ]] || [[ $BUILD_DATE != $BEFORE_DATE ]]; then
        output_log "[booting the first time, so install preinstall apps.]"
        busybox find $PKGS -name "*\.apk" -exec sh /system/bin/pm install -r {} \;
        busybox echo $BUILD_DATE > $MARK
        output_log "[OK, installation complete.]"
    else
        output_log "[not booting the first time, no need to install preinstall apps.]"
    fi
}

check_uninstalled() {
    APPS=("com.homecdn.pdown" "com.homecdn.pservice" "com.komect.video.plive")
    for APP in ${APPS[@]}
    do
        pm list packages -3 | busybox grep -q $APP
        if [ $? != 0 ] && [ -f ${PKGS}${APP}.apk ]; then
            output_log "install" $APP
            pm install ${PKGS}${APP}.apk
        fi
    done
}

upgrade_reinstall
check_uninstalled
