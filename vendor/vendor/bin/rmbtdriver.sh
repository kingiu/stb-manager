#!/system/bin/sh -x
inorrmbtko=prop.rmbtdriver.enable
modules=$(getprop $inorrmbtko)
prop=prop.rmbtdriver.success
COUNT=10
SDIO_POINT="/sys/bus/sdio/devices/*/uevent"
MT7661RS="037A:7663"
get_device_id() {
    for sdio_retry in 1 2 3 4 5
    do
        #echo "===>retry $sdio_retry times" > /dev/ttyAMA0
        sdio_id=`cat $SDIO_POINT | grep SDIO_ID`
        pid_vid=${sdio_id:0-9:9}
        #echo "===>get pid_vid is $pid_vid" > /dev/ttyAMA0
        if [ $pid_vid = $MT7661RS ]; then
            return 0
        else
            return 1
        fi
    done
}
if [ $modules = "true" ];then
    su
    rmmod rtk_btusb
    get_device_id
    #echo "$?" >/dev/ttyAMA0
    if [ $? = 0 ]; then
        rmmod btmtksdio
        if [ $? = 0 ]; then
            setprop $prop true
        fi
    else
        setprop $prop true
    fi
fi
if [ $modules = "false" ];then
    su
    insmod /vendor/lib/modules/btmtksdio.ko
    chmod 660 /dev/stpbt
    chmod 660 /dev/stpbtfwlog
    chown bluetooth bluetooth /dev/stpbt
    chown bluetooth bluetooth /dev/stpbtfwlog
    insmod /vendor/lib/modules/rtk_btusb.ko
fi
