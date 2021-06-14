# Short description

Tool that sends object detection notifications from your cameras to telegram, with image and object type.

# Longer description

This tool runs in daemon mode and listens for the mqtt frigate topic, once the new object is detected it sends snapshot with image to telegram chat id specified in the config. It also has one simple button, after pressing it, the snapshot replaces with movie queried from [motion](https://motion-project.github.io) mysql database in the time of snapshot date.
This was written for the older frigate version that haven't abillity to capture video clips. Now it have, but maybe someone find this userful..


## Install
Edit **telegram-cctv.service** to update full path to program
```
make
cp telegram-cctv.service /etc/systemd/system
systemctl start telegram-cctv
```

## Motion configuration

```
database_type=mysql
database_dbname=dbname
database_host=127.0.0.1
database_port=3306
database_user=user
database_password=password
database_busy_timeout=60
sql_log_movie=on
sql_log_timelapse=off
sql_query="insert into security(camera, name, filename, frame, file_type, time_start) values('%t','%$', '%f', '%q', '%n', '%Y-%m-%d %T')"
sql_query_stop="update security set time_end='%Y-%m-%d %T' where filename='%f'"
```

## Photos

![](/pics/Screenshot%202021-06-10%20at%2014.36.09.png)
![](/pics/Screenshot%202021-06-10%20at%2014.37.35.png)
