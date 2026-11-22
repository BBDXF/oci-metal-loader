# test fixture: prove console logins (autologin on tty1 / ttyS0) by logging a marker from the login shell's profile
case $- in
*i*) m="OMLOADER_LOGIN_OK user=$(id -un) tty=$(tty)"; echo "$m"; echo "$m" > /tmp/omloader-login-$(tty | tr / _) ;;
esac
